// Package postgres — pgx-репозиторий чтения каталога BC#3.
// goca:manual — ADR-007: адаптер возвращает port/out DTO, не domain-типы.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/port/out"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ProgramRepository struct {
	pool *pgxpool.Pool
}

func NewProgramRepository(pool *pgxpool.Pool) *ProgramRepository {
	return &ProgramRepository{pool: pool}
}

func (r *ProgramRepository) List(ctx context.Context, filter out.ProgramFilter) ([]out.ProgramSummary, error) {
	const query = `
SELECT p.id, p.title, p.difficulty, p.aphasia_type, p.version,
       COUNT(l.id) AS total_lessons, p.created_at, p.updated_at
FROM programs p
LEFT JOIN program_lessons l
       ON l.program_id = p.id AND l.program_version = p.version
WHERE ($1 = '' OR p.difficulty = $1)
  AND ($2 = '' OR p.aphasia_type = $2)
GROUP BY p.id
ORDER BY p.created_at DESC, p.id`

	rows, err := r.pool.Query(ctx, query, filter.Difficulty, filter.AphasiaType)
	if err != nil {
		return nil, fmt.Errorf("list programs: %w", err)
	}
	defer rows.Close()

	result := make([]out.ProgramSummary, 0)
	for rows.Next() {
		var item out.ProgramSummary
		if err := rows.Scan(
			&item.ProgramID, &item.Title, &item.Difficulty, &item.AphasiaType,
			&item.Version, &item.TotalLessons, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan program summary: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate programs: %w", err)
	}
	return result, nil
}

func (r *ProgramRepository) Get(ctx context.Context, programID string, version int) (out.ProgramRecord, error) {
	const programQuery = `
SELECT id, title, difficulty, aphasia_type, version, created_at, updated_at
FROM programs WHERE id = $1`

	var record out.ProgramRecord
	if err := r.pool.QueryRow(ctx, programQuery, programID).Scan(
		&record.ProgramID, &record.Title, &record.Difficulty, &record.AphasiaType,
		&record.Version, &record.CreatedAt, &record.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out.ProgramRecord{}, out.ErrNotFound
		}
		return out.ProgramRecord{}, fmt.Errorf("get program: %w", err)
	}

	targetVersion := version
	if targetVersion == 0 {
		targetVersion = record.Version
	}
	if targetVersion > record.Version {
		return out.ProgramRecord{}, out.ErrNotFound
	}

	const lessonsQuery = `
SELECT id, order_position, title, description, content_url, content_hash, created_at, updated_at
FROM program_lessons
WHERE program_id = $1 AND program_version = $2
ORDER BY order_position`

	rows, err := r.pool.Query(ctx, lessonsQuery, programID, targetVersion)
	if err != nil {
		return out.ProgramRecord{}, fmt.Errorf("get program lessons: %w", err)
	}
	defer rows.Close()

	lessons := make([]out.LessonRecord, 0)
	for rows.Next() {
		var lesson out.LessonRecord
		if err := rows.Scan(
			&lesson.LessonID, &lesson.OrderPosition, &lesson.Title, &lesson.Description,
			&lesson.ContentURL, &lesson.ContentHash, &lesson.CreatedAt, &lesson.UpdatedAt,
		); err != nil {
			return out.ProgramRecord{}, fmt.Errorf("scan program lesson: %w", err)
		}
		lessons = append(lessons, lesson)
	}
	if err := rows.Err(); err != nil {
		return out.ProgramRecord{}, fmt.Errorf("iterate program lessons: %w", err)
	}
	if len(lessons) == 0 {
		return out.ProgramRecord{}, out.ErrNotFound
	}

	record.Version = targetVersion
	record.Lessons = lessons
	return record, nil
}

func (r *ProgramRepository) GetLesson(ctx context.Context, programID, lessonID string) (out.LessonRecord, error) {
	const query = `
SELECT id, order_position, title, description, content_url, content_hash, created_at, updated_at
FROM program_lessons
WHERE program_id = $1 AND id = $2
ORDER BY program_version DESC
LIMIT 1`

	var lesson out.LessonRecord
	if err := r.pool.QueryRow(ctx, query, programID, lessonID).Scan(
		&lesson.LessonID, &lesson.OrderPosition, &lesson.Title, &lesson.Description,
		&lesson.ContentURL, &lesson.ContentHash, &lesson.CreatedAt, &lesson.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out.LessonRecord{}, out.ErrNotFound
		}
		return out.LessonRecord{}, fmt.Errorf("get lesson: %w", err)
	}
	return lesson, nil
}
