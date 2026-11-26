// goca:manual — транзакционная запись каталога BC#3 (FOR UPDATE, версии, grandfathering).
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/port/out"
	"github.com/google/uuid"
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

	if err := insertOutbox(ctx, tx, "ProgramCreated", record.ProgramID, map[string]any{
		"programId": record.ProgramID, "title": record.Title,
		"classifier": map[string]string{"difficulty": record.Difficulty, "aphasiaType": record.AphasiaType},
		"version":    record.Version, "created_at": time.Now().UTC(),
	}); err != nil {
		return out.ProgramRecord{}, err
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
	if err := insertOutbox(ctx, tx, "LessonAddedToProgram", programID, map[string]any{
		"programId": programID, "lessonId": inserted.LessonID, "position": inserted.OrderPosition,
		"lessonType": "pdf", "newTotalLessons": total + 1, "contentHash": inserted.ContentHash,
	}); err != nil {
		return out.LessonRecord{}, 0, err
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
		return out.LessonRecord{}, 0, fmt.Errorf("insert_after_position: %w", out.ErrInvalidPosition)
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
	if err := insertOutbox(ctx, tx, "LessonInsertedIntoProgram", programID, map[string]any{
		"programId": programID, "lessonId": inserted.LessonID, "position": inserted.OrderPosition,
		"oldVersion": version, "newVersion": newVersion,
		"totalLessonsOld": total, "newTotalLessons": total + 1,
	}); err != nil {
		return out.LessonRecord{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return out.LessonRecord{}, 0, fmt.Errorf("commit insert lesson: %w", err)
	}
	return inserted, newVersion, nil
}

// Rename меняет только title: classifier неизменяем.
func (r *ProgramRepository) Rename(ctx context.Context, programID, title string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin rename program: %w", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `UPDATE programs SET title = $2, updated_at = now() WHERE id = $1`, programID, title)
	if err != nil {
		return fmt.Errorf("rename program: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return out.ErrNotFound
	}
	if err := insertOutbox(ctx, tx, "ProgramUpdated", programID, map[string]any{
		"programId": programID, "title": title, "updated_at": time.Now().UTC(),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *ProgramRepository) DeleteProgram(ctx context.Context, programID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete program: %w", err)
	}
	defer tx.Rollback(ctx)

	var active int
	if err := tx.QueryRow(ctx, `SELECT active_reference_count FROM programs WHERE id = $1 FOR UPDATE`, programID).Scan(&active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out.ErrNotFound
		}
		return fmt.Errorf("lock program for delete: %w", err)
	}
	if active > 0 {
		return fmt.Errorf("active_patients=%d: %w", active, out.ErrConflict)
	}
	if err := insertOutbox(ctx, tx, "ProgramDeleted", programID, map[string]any{
		"programId": programID, "deleted_at": time.Now().UTC(),
	}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM programs WHERE id = $1`, programID); err != nil {
		return fmt.Errorf("delete program: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete program: %w", err)
	}
	return nil
}

func (r *ProgramRepository) UpdateLesson(ctx context.Context, programID, lessonID string, patch out.LessonPatch) (out.LessonRecord, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return out.LessonRecord{}, fmt.Errorf("begin update lesson: %w", err)
	}
	defer tx.Rollback(ctx)

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
	err = tx.QueryRow(ctx, query, programID, lessonID,
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

	eventType := "LessonUpdated"
	payload := map[string]any{"lessonId": lessonID, "title": lesson.Title, "updated_at": time.Now().UTC()}
	if patch.ContentHash != nil {
		eventType = "LessonContentChanged"
		payload["newContentHash"] = *patch.ContentHash
		payload["contentUrl"] = lesson.ContentURL
	}
	if err := insertOutbox(ctx, tx, eventType, programID, payload); err != nil {
		return out.LessonRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return out.LessonRecord{}, fmt.Errorf("commit update lesson: %w", err)
	}
	return lesson, nil
}

func (r *ProgramRepository) DeleteLesson(ctx context.Context, programID, lessonID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete lesson: %w", err)
	}
	defer tx.Rollback(ctx)

	var active int
	var version int
	if err := tx.QueryRow(ctx, `SELECT version, active_reference_count FROM programs WHERE id = $1 FOR UPDATE`, programID).Scan(&version, &active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out.ErrNotFound
		}
		return fmt.Errorf("lock program for lesson delete: %w", err)
	}
	if active > 0 {
		return fmt.Errorf("active_patients=%d: %w", active, out.ErrConflict)
	}

	var deletedPosition int
	err = tx.QueryRow(ctx, `DELETE FROM program_lessons WHERE program_id = $1 AND program_version = $2 AND id = $3 RETURNING order_position`,
		programID, version, lessonID).Scan(&deletedPosition)
	if errors.Is(err, pgx.ErrNoRows) {
		return out.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("delete lesson: %w", err)
	}

	// Закрываем дыру в нумерации: переносим оставшиеся уроки через temp-таблицу
	// (CHECK 1..200 и PK не дают сдвигать позиции «на месте»).
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE tmp_lessons_shift ON COMMIT DROP AS
SELECT * FROM program_lessons WHERE program_id = $1 AND program_version = $2`, programID, version); err != nil {
		return fmt.Errorf("copy lesson positions: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM program_lessons WHERE program_id = $1 AND program_version = $2`, programID, version); err != nil {
		return fmt.Errorf("clear lesson positions: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO program_lessons
    (id, program_id, program_version, order_position, title, description,
     content_url, content_hash, file_size_bytes, mime_type, uploaded_by, created_at, updated_at)
SELECT id, program_id, program_version,
       ROW_NUMBER() OVER (ORDER BY order_position),
       title, description, content_url, content_hash, file_size_bytes, mime_type,
       uploaded_by, created_at, now()
FROM tmp_lessons_shift`); err != nil {
		return fmt.Errorf("close lesson positions: %w", err)
	}
	if err := insertOutbox(ctx, tx, "LessonRemovedFromProgram", programID, map[string]any{
		"programId": programID, "lessonId": lessonID, "version": version,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete lesson: %w", err)
	}
	return nil
}

func (r *ProgramRepository) CurrentVersion(ctx context.Context, programID string) (int, error) {
	var version int
	err := r.pool.QueryRow(ctx, `SELECT version FROM programs WHERE id = $1`, programID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, out.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("current version: %w", err)
	}
	return version, nil
}

func (r *ProgramRepository) ActiveReferenceCount(ctx context.Context, programID string) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, `SELECT active_reference_count FROM programs WHERE id = $1`, programID).Scan(&count)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, out.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("active reference count: %w", err)
	}
	return count, nil
}

// CreateMigrationRequest пишет MigrationRequested в outbox в одной транзакции
// с проверкой целевой версии.
func (r *ProgramRepository) CreateMigrationRequest(ctx context.Context, programID string, targetVersion int, selection string, accountIDs []string) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin migration request: %w", err)
	}
	defer tx.Rollback(ctx)

	var currentVersion int
	if err := tx.QueryRow(ctx, `SELECT version FROM programs WHERE id = $1 FOR UPDATE`, programID).Scan(&currentVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", out.ErrNotFound
		}
		return "", fmt.Errorf("lock program for migration: %w", err)
	}
	if targetVersion < 1 || targetVersion > currentVersion {
		return "", out.ErrNotFound
	}

	var lessons int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM program_lessons WHERE program_id = $1 AND program_version = $2`,
		programID, targetVersion).Scan(&lessons); err != nil {
		return "", fmt.Errorf("count target version lessons: %w", err)
	}
	if lessons == 0 {
		return "", out.ErrNotFound
	}

	jobID := migrationJobID(programID, currentVersion, targetVersion, selection, accountIDs)
	canonicalIDs := canonicalAccountIDs(accountIDs)
	if err := insertOutboxWithID(ctx, tx, jobID, "MigrationRequested", programID, map[string]any{
		"migrationJobId":   jobID,
		"programId":        programID,
		"oldVersion":       currentVersion,
		"targetVersion":    targetVersion,
		"patientSelection": selection,
		"accountIds":       canonicalIDs,
	}); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "outbox_pkey" {
			// Идемпотентный повтор в те же UTC-сутки: событие уже поставлено,
			// возвращаем тот же job id (ADR-011).
			return jobID, nil
		}
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit migration request: %w", err)
	}
	return jobID, nil
}

// migrationJobID детерминирован для (program, oldVersion, target, selection,
// набор accountIds, UTC-день). oldVersion входит в ключ: после изменения
// программы тот же target — уже другая миграция.
func migrationJobID(programID string, oldVersion, targetVersion int, selection string, accountIDs []string) string {
	key := strings.Join([]string{
		programID,
		strconv.Itoa(oldVersion),
		strconv.Itoa(targetVersion),
		selection,
		strings.Join(canonicalAccountIDs(accountIDs), ","),
		time.Now().UTC().Format("2006-01-02"),
	}, "|")
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String()
}

func canonicalAccountIDs(accountIDs []string) []string {
	canonical := append([]string(nil), accountIDs...)
	sort.Strings(canonical)
	return canonical
}

// ActiveReferenceTotal — сумма active_reference_count по всем программам.
func (r *ProgramRepository) ActiveReferenceTotal(ctx context.Context) (int, error) {
	var total int
	if err := r.pool.QueryRow(ctx, `SELECT COALESCE(SUM(active_reference_count), 0) FROM programs`).Scan(&total); err != nil {
		return 0, fmt.Errorf("active reference total: %w", err)
	}
	return total, nil
}

// ProgramCounts — количество программ по классификатору (ключ "difficulty|aphasia_type").
func (r *ProgramRepository) ProgramCounts(ctx context.Context) (map[string]int, error) {
	rows, err := r.pool.Query(ctx, `SELECT difficulty, aphasia_type, COUNT(*) FROM programs GROUP BY difficulty, aphasia_type`)
	if err != nil {
		return nil, fmt.Errorf("program counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var difficulty, aphasia string
		var count int
		if err := rows.Scan(&difficulty, &aphasia, &count); err != nil {
			return nil, fmt.Errorf("scan program counts: %w", err)
		}
		counts[difficulty+"|"+aphasia] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("program counts rows: %w", err)
	}
	return counts, nil
}

// LessonCounts — количество уроков в текущей версии каждой программы.
func (r *ProgramRepository) LessonCounts(ctx context.Context) (map[string]int, error) {
	rows, err := r.pool.Query(ctx, `
SELECT pl.program_id, COUNT(*)
FROM program_lessons pl
JOIN programs p ON p.id = pl.program_id AND p.version = pl.program_version
GROUP BY pl.program_id`)
	if err != nil {
		return nil, fmt.Errorf("lesson counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var programID string
		var count int
		if err := rows.Scan(&programID, &count); err != nil {
			return nil, fmt.Errorf("scan lesson counts: %w", err)
		}
		counts[programID] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("lesson counts rows: %w", err)
	}
	return counts, nil
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

func insertOutbox(ctx context.Context, tx pgx.Tx, eventType, aggregateID string, payload map[string]any) error {
	return insertOutboxWithID(ctx, tx, "", eventType, aggregateID, payload)
}

// insertOutboxWithID позволяет задать детерминированный id (идемпотентность);
// пустой id означает gen_random_uuid().
func insertOutboxWithID(ctx context.Context, tx pgx.Tx, id, eventType, aggregateID string, payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal outbox payload: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO outbox (id, event_type, aggregate_id, payload)
VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()), $2, $3, $4)`,
		id, eventType, aggregateID, raw); err != nil {
		return fmt.Errorf("insert outbox %s: %w", eventType, err)
	}
	return nil
}

func mapWriteError(op string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		if pgErr.ConstraintName == "uniq_programs_classifier" {
			return fmt.Errorf("classifier: %w", out.ErrConflict)
		}
		return fmt.Errorf("%s: %w", op, out.ErrConflict)
	}
	return fmt.Errorf("%s: %w", op, err)
}
