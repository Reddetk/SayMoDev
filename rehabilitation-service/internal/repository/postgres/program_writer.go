// goca:manual — транзакционная запись каталога BC#3 (FOR UPDATE, версии, grandfathering).
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/port/out"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const maxLessons = 200

func (r *ProgramRepository) Create(ctx context.Context, cmd out.CreateProgramCmd) (out.ProgramRecord, error) {
	if len(cmd.Lessons) == 0 || len(cmd.Lessons) > maxLessons {
		return out.ProgramRecord{}, fmt.Errorf("lessons: %w", out.ErrConflict)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return out.ProgramRecord{}, fmt.Errorf("begin create program: %w", err)
	}
	defer tx.Rollback(ctx)

	var record out.ProgramRecord
	err = tx.QueryRow(ctx, `
INSERT INTO programs (id, title, difficulty, aphasia_type, version)
VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()), $2, $3, $4, 1)
RETURNING id, title, difficulty, aphasia_type, version, created_at, updated_at`,
		cmd.ID, cmd.Title, cmd.Difficulty, cmd.AphasiaType,
	).Scan(&record.ProgramID, &record.Title, &record.Difficulty, &record.AphasiaType,
		&record.Version, &record.CreatedAt, &record.UpdatedAt)
	if err != nil {
		return out.ProgramRecord{}, mapWriteError("create program", err)
	}

	record.Lessons = make([]out.LessonRecord, 0, len(cmd.Lessons))
	for i, lesson := range cmd.Lessons {
		inserted, err := insertLesson(ctx, tx, record.ProgramID, 1, i+1, lesson)
		if err != nil {
			return out.ProgramRecord{}, mapWriteError("create lesson", err)
		}
		record.Lessons = append(record.Lessons, inserted)
	}

	if err := tx.Commit(ctx); err != nil {
		return out.ProgramRecord{}, fmt.Errorf("commit create program: %w", err)
	}
	return record, nil
}

func (r *ProgramRepository) AppendLesson(ctx context.Context, programID string, lesson out.LessonWrite) (out.LessonRecord, int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return out.LessonRecord{}, 0, fmt.Errorf("begin append lesson: %w", err)
	}
	defer tx.Rollback(ctx)

	version, total, err := lockProgram(ctx, tx, programID)
	if err != nil {
		return out.LessonRecord{}, 0, err
	}
	if total >= maxLessons {
		return out.LessonRecord{}, 0, fmt.Errorf("lessons limit: %w", out.ErrConflict)
	}

	inserted, err := insertLesson(ctx, tx, programID, version, total+1, lesson)
	if err != nil {
		return out.LessonRecord{}, 0, mapWriteError("append lesson", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return out.LessonRecord{}, 0, fmt.Errorf("commit append lesson: %w", err)
	}
	return inserted, version, nil
}

func (r *ProgramRepository) InsertLessonAfter(ctx context.Context, programID string, after int, lesson out.LessonWrite) (out.LessonRecord, int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return out.LessonRecord{}, 0, fmt.Errorf("begin insert lesson: %w", err)
	}
	defer tx.Rollback(ctx)

	version, total, err := lockProgram(ctx, tx, programID)
	if err != nil {
		return out.LessonRecord{}, 0, err
	}
	if after < 1 || after >= total {
		return out.LessonRecord{}, 0, fmt.Errorf("insert_after_position: %w", out.ErrConflict)
	}
	if total >= maxLessons {
		return out.LessonRecord{}, 0, fmt.Errorf("lessons limit: %w", out.ErrConflict)
	}

	newVersion := version + 1
	if _, err := tx.Exec(ctx, `
INSERT INTO program_lessons
    (id, program_id, program_version, order_position, title, description,
     content_url, content_hash, file_size_bytes, mime_type, uploaded_by, created_at, updated_at)
SELECT id, program_id, $2,
       order_position + CASE WHEN order_position > $3 THEN 1 ELSE 0 END,
       title, description, content_url, content_hash, file_size_bytes, mime_type,
       uploaded_by, created_at, now()
FROM program_lessons
WHERE program_id = $1 AND program_version = $4`,
		programID, newVersion, after, version,
	); err != nil {
		return out.LessonRecord{}, 0, mapWriteError("copy lesson version", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE programs SET version = $2, updated_at = now() WHERE id = $1`, programID, newVersion); err != nil {
		return out.LessonRecord{}, 0, fmt.Errorf("bump program version: %w", err)
	}

	inserted, err := insertLesson(ctx, tx, programID, newVersion, after+1, lesson)
	if err != nil {
		return out.LessonRecord{}, 0, mapWriteError("insert lesson", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return out.LessonRecord{}, 0, fmt.Errorf("commit insert lesson: %w", err)
	}
	return inserted, newVersion, nil
}

// Rename меняет только title: classifier неизменяем.
func (r *ProgramRepository) Rename(ctx context.Context, programID, title string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE programs SET title = $2, updated_at = now() WHERE id = $1`, programID, title)
	if err != nil {
		return fmt.Errorf("rename program: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return out.ErrNotFound
	}
	return nil
}

func (r *ProgramRepository) DeleteProgram(ctx context.Context, programID string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM programs WHERE id = $1`, programID)
	if err != nil {
		return fmt.Errorf("delete program: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return out.ErrNotFound
	}
	return nil
}

func (r *ProgramRepository) UpdateLesson(ctx context.Context, programID, lessonID string, patch out.LessonPatch) (out.LessonRecord, error) {
	const query = `
UPDATE program_lessons SET
    title           = COALESCE($3, title),
    description     = COALESCE($4, description),
    content_url     = COALESCE($5, content_url),
    content_hash    = COALESCE($6, content_hash),
    file_size_bytes = COALESCE($7, file_size_bytes),
    mime_type       = COALESCE($8, mime_type),
    updated_at      = now()
WHERE program_id = $1 AND id = $2
RETURNING id, order_position, title, description, content_url, content_hash, created_at, updated_at`

	var lesson out.LessonRecord
	err := r.pool.QueryRow(ctx, query, programID, lessonID,
		patch.Title, patch.Description, patch.ContentURL, patch.ContentHash,
		patch.FileSizeBytes, patch.MimeType,
	).Scan(&lesson.LessonID, &lesson.OrderPosition, &lesson.Title, &lesson.Description,
		&lesson.ContentURL, &lesson.ContentHash, &lesson.CreatedAt, &lesson.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out.LessonRecord{}, out.ErrNotFound
		}
		return out.LessonRecord{}, mapWriteError("update lesson", err)
	}
	return lesson, nil
}

func (r *ProgramRepository) DeleteLesson(ctx context.Context, programID, lessonID string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM program_lessons WHERE program_id = $1 AND id = $2`, programID, lessonID)
	if err != nil {
		return fmt.Errorf("delete lesson: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return out.ErrNotFound
	}
	return nil
}

func (r *ProgramRepository) FindContentByHash(ctx context.Context, contentHash string) (string, bool, error) {
	var contentURL string
	err := r.pool.QueryRow(ctx, `
SELECT content_url FROM program_lessons WHERE content_hash = $1 ORDER BY created_at DESC LIMIT 1`,
		contentHash,
	).Scan(&contentURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("find content by hash: %w", err)
	}
	return contentURL, true, nil
}

func lockProgram(ctx context.Context, tx pgx.Tx, programID string) (version, total int, err error) {
	err = tx.QueryRow(ctx, `SELECT version FROM programs WHERE id = $1 FOR UPDATE`, programID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, out.ErrNotFound
	}
	if err != nil {
		return 0, 0, fmt.Errorf("lock program: %w", err)
	}

	if err := tx.QueryRow(ctx, `
SELECT COUNT(*) FROM program_lessons WHERE program_id = $1 AND program_version = $2`,
		programID, version,
	).Scan(&total); err != nil {
		return 0, 0, fmt.Errorf("count lessons: %w", err)
	}
	return version, total, nil
}

func insertLesson(ctx context.Context, tx pgx.Tx, programID string, version, position int, lesson out.LessonWrite) (out.LessonRecord, error) {
	var record out.LessonRecord
	err := tx.QueryRow(ctx, `
INSERT INTO program_lessons
    (id, program_id, program_version, order_position, title, description,
     content_url, content_hash, file_size_bytes, mime_type, uploaded_by)
VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()), $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING id, order_position, title, description, content_url, content_hash, created_at, updated_at`,
		lesson.ID, programID, version, position, lesson.Title, lesson.Description,
		lesson.ContentURL, lesson.ContentHash, lesson.FileSizeBytes, lesson.MimeType, lesson.UploadedBy,
	).Scan(&record.LessonID, &record.OrderPosition, &record.Title, &record.Description,
		&record.ContentURL, &record.ContentHash, &record.CreatedAt, &record.UpdatedAt)
	return record, err
}

func mapWriteError(op string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("%s: %w", op, out.ErrConflict)
	}
	return fmt.Errorf("%s: %w", op, err)
}
