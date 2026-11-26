// Package postgres implements secondary persistence ports with pgx/v5.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	corerr "github.com/Reddetk/SayMoDev/progress-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

// Repo is the pgx-backed repository and unit of work.
type Repo struct {
	pool *pgxpool.Pool
}

// New opens a connection pool and verifies connectivity.
func New(ctx context.Context, dsn string, maxConns, minConns int, maxLifetime time.Duration) (*Repo, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse dsn: %w", err)
	}
	cfg.MaxConns = int32(maxConns)
	cfg.MinConns = int32(minConns)
	cfg.MaxConnLifetime = maxLifetime

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return &Repo{pool: pool}, nil
}

// Close releases the pool.
func (r *Repo) Close() { r.pool.Close() }

// Ping checks connectivity for readiness probes.
func (r *Repo) Ping(ctx context.Context) error { return r.pool.Ping(ctx) }

// Do runs fn inside one transaction (rollback on error or panic).
func (r *Repo) Do(ctx context.Context, fn func(ctx context.Context, tx out.Tx) error) (err error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	if err = fn(ctx, &sqlTx{tx: tx}); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit: %w", err)
	}
	return nil
}

const progressColumns = `account_id, program_id, program_version, status, completed_lessons, total_lessons,
	completion_percent, week_bitmap, timezone, tariff_valid_until, tariff_activated_at, last_completed_at, last_updated_at`

func scanProgress(row pgx.Row) (out.ProgressRecord, error) {
	var p out.ProgressRecord
	var week *int
	var tz *string
	var validUntil, activatedAt *time.Time
	err := row.Scan(&p.AccountID, &p.ProgramID, &p.ProgramVersion, &p.Status, &p.CompletedLessons, &p.TotalLessons,
		&p.CompletionPercent, &week, &tz, &validUntil, &activatedAt, &p.LastCompletedAt, &p.LastUpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out.ProgressRecord{}, corerr.ErrNoActiveProgram
		}
		return out.ProgressRecord{}, err
	}
	if week != nil && tz != nil {
		act := time.Time{}
		if activatedAt != nil {
			act = *activatedAt
		}
		p.Tariff = &out.TariffSnapshotRecord{WeekBitmap: *week, Timezone: *tz, ValidUntil: validUntil, ActivatedAt: act}
	}
	return p, nil
}

func (r *Repo) GetCurrentProgress(ctx context.Context, accountID uuid.UUID) (*out.ProgressRecord, error) {
	const q = `SELECT ` + progressColumns + ` FROM patient_progress WHERE account_id = $1
		ORDER BY CASE status WHEN 'active' THEN 0 WHEN 'completed' THEN 1 ELSE 2 END, last_updated_at DESC LIMIT 1`
	p, err := scanProgress(r.pool.QueryRow(ctx, q, accountID))
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *Repo) GetProgressByProgram(ctx context.Context, accountID, programID uuid.UUID) (*out.ProgressRecord, error) {
	const q = `SELECT ` + progressColumns + ` FROM patient_progress WHERE account_id = $1 AND program_id = $2`
	p, err := scanProgress(r.pool.QueryRow(ctx, q, accountID, programID))
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *Repo) ListProgress(ctx context.Context, accountID uuid.UUID) ([]out.ProgressRecord, error) {
	const q = `SELECT ` + progressColumns + ` FROM patient_progress WHERE account_id = $1 ORDER BY last_updated_at DESC`
	return r.queryProgress(ctx, r.pool, q, accountID)
}

func (r *Repo) ListProgressByProgram(ctx context.Context, programID uuid.UUID) ([]out.ProgressRecord, error) {
	const q = `SELECT ` + progressColumns + ` FROM patient_progress WHERE program_id = $1 ORDER BY last_updated_at DESC`
	return r.queryProgress(ctx, r.pool, q, programID)
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// GetAccountTariff returns the account-level snapshot (pending before activation).
func (r *Repo) GetAccountTariff(ctx context.Context, accountID uuid.UUID) (*out.TariffSnapshotRecord, error) {
	return queryAccountTariff(ctx, r.pool, accountID)
}

func queryAccountTariff(ctx context.Context, q querier, accountID uuid.UUID) (*out.TariffSnapshotRecord, error) {
	var rec out.TariffSnapshotRecord
	err := q.QueryRow(ctx, `SELECT week_bitmap, timezone, valid_until, activated_at
		FROM account_tariffs WHERE account_id = $1`, accountID).
		Scan(&rec.WeekBitmap, &rec.Timezone, &rec.ValidUntil, &rec.ActivatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func (r *Repo) queryProgress(ctx context.Context, q querier, sql string, args ...any) ([]out.ProgressRecord, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []out.ProgressRecord
	for rows.Next() {
		p, err := scanProgress(rows)
		if err != nil {
			return nil, err
		}
		res = append(res, p)
	}
	return res, rows.Err()
}

const accessibleColumns = `account_id, lesson_id, program_id, lesson_type, order_position, granted_at, revoked_at`

func scanAccessible(row pgx.Row) (out.AccessibleLessonRecord, error) {
	var a out.AccessibleLessonRecord
	err := row.Scan(&a.AccountID, &a.LessonID, &a.ProgramID, &a.LessonType, &a.OrderPosition, &a.GrantedAt, &a.RevokedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out.AccessibleLessonRecord{}, corerr.ErrNoActiveProgram
		}
		return out.AccessibleLessonRecord{}, err
	}
	return a, nil
}

func (r *Repo) GetAccessibleLesson(ctx context.Context, accountID uuid.UUID) (*out.AccessibleLessonRecord, error) {
	const q = `SELECT ` + accessibleColumns + ` FROM accessible_lessons WHERE account_id = $1 AND revoked_at IS NULL LIMIT 1`
	a, err := scanAccessible(r.pool.QueryRow(ctx, q, accountID))
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *Repo) ListAccessibleLessons(ctx context.Context, accountID uuid.UUID) ([]out.AccessibleLessonRecord, error) {
	const q = `SELECT ` + accessibleColumns + ` FROM accessible_lessons WHERE account_id = $1 ORDER BY granted_at DESC LIMIT 50`
	rows, err := r.pool.Query(ctx, q, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []out.AccessibleLessonRecord
	for rows.Next() {
		a, err := scanAccessible(rows)
		if err != nil {
			return nil, err
		}
		res = append(res, a)
	}
	return res, rows.Err()
}

const programColumns = `account_id, program_id, version, status, difficulty, aphasia_type, activated_at,
	completed_at, paused_at, completion_percent`

func scanProgram(row pgx.Row) (out.ActiveProgramRecord, error) {
	var p out.ActiveProgramRecord
	err := row.Scan(&p.AccountID, &p.ProgramID, &p.Version, &p.Status, &p.Difficulty, &p.AphasiaType,
		&p.ActivatedAt, &p.CompletedAt, &p.PausedAt, &p.CompletionPercent)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out.ActiveProgramRecord{}, corerr.ErrNoActiveProgram
		}
		return out.ActiveProgramRecord{}, err
	}
	return p, nil
}

func (r *Repo) ListPrograms(ctx context.Context, accountID uuid.UUID) ([]out.ActiveProgramRecord, error) {
	const q = `SELECT ` + programColumns + ` FROM active_programs WHERE account_id = $1 ORDER BY activated_at DESC`
	rows, err := r.pool.Query(ctx, q, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []out.ActiveProgramRecord
	for rows.Next() {
		p, err := scanProgram(rows)
		if err != nil {
			return nil, err
		}
		res = append(res, p)
	}
	return res, rows.Err()
}

func (r *Repo) GetActiveProgram(ctx context.Context, accountID uuid.UUID) (*out.ActiveProgramRecord, error) {
	const q = `SELECT ` + programColumns + ` FROM active_programs WHERE account_id = $1 AND status = 'active' LIMIT 1`
	p, err := scanProgram(r.pool.QueryRow(ctx, q, accountID))
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *Repo) CountAnswers(ctx context.Context, accountID, lessonID uuid.UUID) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM lesson_answers WHERE account_id = $1 AND lesson_id = $2`, accountID, lessonID).Scan(&n)
	return n, err
}

// HasCompletion reports whether the lesson was already finalised (irreversible).
func (r *Repo) HasCompletion(ctx context.Context, accountID, lessonID uuid.UUID) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM lesson_completions WHERE account_id=$1 AND lesson_id=$2)`,
		accountID, lessonID).Scan(&exists)
	return exists, err
}

func (r *Repo) ListAnswers(ctx context.Context, f out.AnswerFilter) (out.AnswerPage, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	order := "DESC"
	if f.Ascending {
		order = "ASC"
	}
	args := []any{f.AccountID}
	where := `account_id = $1`
	if f.LessonID != nil {
		args = append(args, *f.LessonID)
		where += fmt.Sprintf(" AND lesson_id = $%d", len(args))
	}
	if f.MediaType != "" {
		args = append(args, f.MediaType)
		where += fmt.Sprintf(" AND media_type = $%d", len(args))
	}
	var total int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM lesson_answers WHERE `+where, args...).Scan(&total); err != nil {
		return out.AnswerPage{}, err
	}
	args = append(args, limit, offset)
	q := fmt.Sprintf(`SELECT answer_id, account_id, lesson_id, program_id, media_type, payload, submitted_at, anonymised
		FROM lesson_answers WHERE %s ORDER BY submitted_at %s LIMIT $%d OFFSET $%d`, where, order, len(args)-1, len(args))
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return out.AnswerPage{}, err
	}
	defer rows.Close()
	page := out.AnswerPage{Total: total}
	for rows.Next() {
		var a out.AnswerRecord
		if err := rows.Scan(&a.AnswerID, &a.AccountID, &a.LessonID, &a.ProgramID, &a.MediaType, &a.Payload, &a.SubmittedAt, &a.Anonymised); err != nil {
			return out.AnswerPage{}, err
		}
		page.Answers = append(page.Answers, a)
	}
	return page, rows.Err()
}

func (r *Repo) CountActivePatients(ctx context.Context, programID uuid.UUID, version int) (out.ActivePatients, error) {
	const q = `SELECT DISTINCT account_id FROM patient_progress
		WHERE program_id = $1 AND program_version = $2 AND status = 'active'`
	rows, err := r.pool.Query(ctx, q, programID, version)
	if err != nil {
		return out.ActivePatients{}, err
	}
	defer rows.Close()
	res := out.ActivePatients{ProgramID: programID, Version: version}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return out.ActivePatients{}, err
		}
		res.IDs = append(res.IDs, id)
	}
	res.Count = len(res.IDs)
	return res, rows.Err()
}

func (r *Repo) FetchUnsentOutbox(ctx context.Context, limit int) ([]out.OutboxEvent, error) {
	const q = `SELECT id, event_id, event_type, event_version, correlation_id, payload, occurred_at FROM outbox
		WHERE sent_at IS NULL ORDER BY occurred_at LIMIT $1 FOR UPDATE SKIP LOCKED`
	rows, err := r.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []out.OutboxEvent
	for rows.Next() {
		var e out.OutboxEvent
		if err := rows.Scan(&e.ID, &e.EventID, &e.EventType, &e.EventVersion, &e.CorrelationID, &e.Payload, &e.OccurredAt); err != nil {
			return nil, err
		}
		res = append(res, e)
	}
	return res, rows.Err()
}

func (r *Repo) MarkOutboxSent(ctx context.Context, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx, `UPDATE outbox SET sent_at = now() WHERE id = ANY($1)`, ids)
	return err
}

// ActiveProgramCount feeds the active_programs_count gauge.
func (r *Repo) ActiveProgramCount(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM active_programs WHERE status = 'active'`).Scan(&n)
	return n, err
}

// isUniqueViolation detects PostgreSQL unique index violations.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
