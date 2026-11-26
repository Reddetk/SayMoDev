package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	corerr "github.com/Reddetk/SayMoDev/progress-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

type sqlTx struct {
	tx pgx.Tx
}

func (s *sqlTx) GetProgressForUpdate(ctx context.Context, accountID uuid.UUID) (*out.ProgressRecord, error) {
	const q = `SELECT ` + progressColumns + ` FROM patient_progress WHERE account_id = $1
		ORDER BY CASE status WHEN 'active' THEN 0 WHEN 'completed' THEN 1 ELSE 2 END, last_updated_at DESC
		LIMIT 1 FOR UPDATE`
	p, err := scanProgress(s.tx.QueryRow(ctx, q, accountID))
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *sqlTx) GetProgressByProgramForUpdate(ctx context.Context, accountID, programID uuid.UUID) (*out.ProgressRecord, error) {
	const q = `SELECT ` + progressColumns + ` FROM patient_progress
		WHERE account_id = $1 AND program_id = $2 FOR UPDATE`
	p, err := scanProgress(s.tx.QueryRow(ctx, q, accountID, programID))
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *sqlTx) ListProgress(ctx context.Context, accountID uuid.UUID) ([]out.ProgressRecord, error) {
	const q = `SELECT ` + progressColumns + ` FROM patient_progress WHERE account_id = $1 ORDER BY last_updated_at DESC`
	return s.queryProgress(ctx, q, accountID)
}

func (s *sqlTx) ListProgressByProgramForUpdate(ctx context.Context, programID uuid.UUID) ([]out.ProgressRecord, error) {
	const q = `SELECT ` + progressColumns + ` FROM patient_progress WHERE program_id = $1 FOR UPDATE`
	return s.queryProgress(ctx, q, programID)
}

func (s *sqlTx) queryProgress(ctx context.Context, q string, args ...any) ([]out.ProgressRecord, error) {
	rows, err := s.tx.Query(ctx, q, args...)
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

func (s *sqlTx) InsertProgress(ctx context.Context, rec out.ProgressRecord) error {
	var week *int
	var tz *string
	var validUntil, activatedAt *time.Time
	if rec.Tariff != nil {
		week = &rec.Tariff.WeekBitmap
		tz = &rec.Tariff.Timezone
		validUntil = rec.Tariff.ValidUntil
		activatedAt = &rec.Tariff.ActivatedAt
	}
	_, err := s.tx.Exec(ctx, `INSERT INTO patient_progress (account_id, program_id, program_version, status,
		completed_lessons, total_lessons, completion_percent, week_bitmap, timezone, tariff_valid_until,
		tariff_activated_at, last_completed_at, last_updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (account_id, program_id) DO UPDATE SET
			program_version=EXCLUDED.program_version, status=EXCLUDED.status,
			completed_lessons=EXCLUDED.completed_lessons, total_lessons=EXCLUDED.total_lessons,
			completion_percent=EXCLUDED.completion_percent, week_bitmap=EXCLUDED.week_bitmap,
			timezone=EXCLUDED.timezone, tariff_valid_until=EXCLUDED.tariff_valid_until,
			tariff_activated_at=EXCLUDED.tariff_activated_at,
			last_completed_at=EXCLUDED.last_completed_at, last_updated_at=EXCLUDED.last_updated_at`,
		rec.AccountID, rec.ProgramID, rec.ProgramVersion, rec.Status, rec.CompletedLessons, rec.TotalLessons,
		rec.CompletionPercent, week, tz, validUntil, activatedAt, rec.LastCompletedAt, rec.LastUpdatedAt)
	return err
}

func (s *sqlTx) UpdateProgress(ctx context.Context, rec out.ProgressRecord) error {
	var week *int
	var tz *string
	var validUntil, activatedAt *time.Time
	if rec.Tariff != nil {
		week = &rec.Tariff.WeekBitmap
		tz = &rec.Tariff.Timezone
		validUntil = rec.Tariff.ValidUntil
		activatedAt = &rec.Tariff.ActivatedAt
	}
	_, err := s.tx.Exec(ctx, `UPDATE patient_progress SET program_version=$3, status=$4, completed_lessons=$5,
		total_lessons=$6, completion_percent=$7, week_bitmap=$8, timezone=$9, tariff_valid_until=$10,
		tariff_activated_at=$11, last_completed_at=$12, last_updated_at=$13
		WHERE account_id=$1 AND program_id=$2`,
		rec.AccountID, rec.ProgramID, rec.ProgramVersion, rec.Status, rec.CompletedLessons, rec.TotalLessons,
		rec.CompletionPercent, week, tz, validUntil, activatedAt, rec.LastCompletedAt, rec.LastUpdatedAt)
	return err
}

func (s *sqlTx) GetActiveProgramForUpdate(ctx context.Context, accountID uuid.UUID) (*out.ActiveProgramRecord, error) {
	const q = `SELECT ` + programColumns + ` FROM active_programs
		WHERE account_id = $1 AND status = 'active' LIMIT 1 FOR UPDATE`
	p, err := scanProgram(s.tx.QueryRow(ctx, q, accountID))
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *sqlTx) InsertActiveProgram(ctx context.Context, rec out.ActiveProgramRecord) error {
	_, err := s.tx.Exec(ctx, `INSERT INTO active_programs (account_id, program_id, version, status, difficulty,
		aphasia_type, activated_at, completed_at, paused_at, completion_percent)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (account_id, program_id) DO UPDATE SET
			version=EXCLUDED.version, status=EXCLUDED.status, difficulty=EXCLUDED.difficulty,
			aphasia_type=EXCLUDED.aphasia_type, activated_at=EXCLUDED.activated_at,
			completed_at=EXCLUDED.completed_at, paused_at=EXCLUDED.paused_at,
			completion_percent=EXCLUDED.completion_percent`,
		rec.AccountID, rec.ProgramID, rec.Version, rec.Status, rec.Difficulty, rec.AphasiaType,
		rec.ActivatedAt, rec.CompletedAt, rec.PausedAt, rec.CompletionPercent)
	if isUniqueViolation(err) {
		return corerr.ErrProgramAlreadyActive
	}
	return err
}

func (s *sqlTx) UpdateActiveProgram(ctx context.Context, rec out.ActiveProgramRecord) error {
	_, err := s.tx.Exec(ctx, `UPDATE active_programs SET version=$3, status=$4, difficulty=$5, aphasia_type=$6,
		activated_at=$7, completed_at=$8, paused_at=$9, completion_percent=$10
		WHERE account_id=$1 AND program_id=$2`,
		rec.AccountID, rec.ProgramID, rec.Version, rec.Status, rec.Difficulty, rec.AphasiaType,
		rec.ActivatedAt, rec.CompletedAt, rec.PausedAt, rec.CompletionPercent)
	if isUniqueViolation(err) {
		return corerr.ErrProgramAlreadyActive
	}
	return err
}

func (s *sqlTx) ListPrograms(ctx context.Context, accountID uuid.UUID) ([]out.ActiveProgramRecord, error) {
	const q = `SELECT ` + programColumns + ` FROM active_programs WHERE account_id = $1 ORDER BY activated_at DESC FOR UPDATE`
	rows, err := s.tx.Query(ctx, q, accountID)
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

func (s *sqlTx) GetAccessibleLessonForUpdate(ctx context.Context, accountID uuid.UUID) (*out.AccessibleLessonRecord, error) {
	const q = `SELECT ` + accessibleColumns + ` FROM accessible_lessons
		WHERE account_id = $1 AND revoked_at IS NULL LIMIT 1 FOR UPDATE`
	a, err := scanAccessible(s.tx.QueryRow(ctx, q, accountID))
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *sqlTx) InsertAccessibleLesson(ctx context.Context, rec out.AccessibleLessonRecord) error {
	_, err := s.tx.Exec(ctx, `INSERT INTO accessible_lessons (account_id, lesson_id, program_id, lesson_type,
		order_position, granted_at, revoked_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		rec.AccountID, rec.LessonID, rec.ProgramID, rec.LessonType, rec.OrderPosition, rec.GrantedAt, rec.RevokedAt)
	if isUniqueViolation(err) {
		return corerr.ErrLessonNotAccessible
	}
	return err
}

func (s *sqlTx) RevokeAccessibleLessons(ctx context.Context, accountID uuid.UUID, at time.Time) (int64, error) {
	tag, err := s.tx.Exec(ctx, `UPDATE accessible_lessons SET revoked_at = $2 WHERE account_id = $1 AND revoked_at IS NULL`, accountID, at)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *sqlTx) HasCompletion(ctx context.Context, accountID, lessonID uuid.UUID) (bool, error) {
	var exists bool
	err := s.tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM lesson_completions WHERE account_id=$1 AND lesson_id=$2)`,
		accountID, lessonID).Scan(&exists)
	return exists, err
}

func (s *sqlTx) CountAnswers(ctx context.Context, accountID, lessonID uuid.UUID) (int, error) {
	var n int
	err := s.tx.QueryRow(ctx, `SELECT count(*) FROM lesson_answers WHERE account_id=$1 AND lesson_id=$2`, accountID, lessonID).Scan(&n)
	return n, err
}

func (s *sqlTx) InsertCompletion(ctx context.Context, rec out.CompletionRecord) error {
	_, err := s.tx.Exec(ctx, `INSERT INTO lesson_completions (account_id, lesson_id, program_id, completed_at)
		VALUES ($1,$2,$3,$4)`, rec.AccountID, rec.LessonID, rec.ProgramID, rec.CompletedAt)
	if isUniqueViolation(err) {
		return corerr.ErrLessonAlreadyCompleted
	}
	return err
}

func (s *sqlTx) InsertAnswer(ctx context.Context, rec out.AnswerRecord) error {
	_, err := s.tx.Exec(ctx, `INSERT INTO lesson_answers (answer_id, account_id, lesson_id, program_id, media_type,
		payload, submitted_at, anonymised) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		rec.AnswerID, rec.AccountID, rec.LessonID, rec.ProgramID, rec.MediaType, rec.Payload, rec.SubmittedAt, rec.Anonymised)
	return err
}

func (s *sqlTx) InsertSnapshot(ctx context.Context, rec out.SnapshotRecord) error {
	_, err := s.tx.Exec(ctx, `INSERT INTO progress_snapshots (account_id, program_id, completion_percent,
		snapshot_reason, created_at) VALUES ($1,$2,$3,$4,$5)`,
		rec.AccountID, rec.ProgramID, rec.CompletionPercent, rec.Reason, rec.CreatedAt)
	return err
}

func (s *sqlTx) InsertOutbox(ctx context.Context, ev out.OutboxEvent) error {
	version := ev.EventVersion
	if version <= 0 {
		version = 1
	}
	_, err := s.tx.Exec(ctx, `INSERT INTO outbox (id, event_id, event_type, event_version, correlation_id, payload, occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, ev.ID, ev.EventID, ev.EventType, version, ev.CorrelationID, ev.Payload, ev.OccurredAt)
	return err
}

func (s *sqlTx) RecordIdempotency(ctx context.Context, rec out.IdempotencyRecord) (bool, error) {
	tag, err := s.tx.Exec(ctx, `INSERT INTO idempotency_keys (event_id, expires_at) VALUES ($1,$2)
		ON CONFLICT (event_id) DO NOTHING`, rec.EventID, rec.ExpiresAt)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// GetAccountTariff returns the account-level snapshot used when a tariff arrives before activation.
func (s *sqlTx) GetAccountTariff(ctx context.Context, accountID uuid.UUID) (*out.TariffSnapshotRecord, error) {
	return queryAccountTariff(ctx, s.tx, accountID)
}

// UpsertAccountTariff writes the pending/canonical snapshot; nil clears it.
func (s *sqlTx) UpsertAccountTariff(ctx context.Context, accountID uuid.UUID, rec *out.TariffSnapshotRecord) error {
	if rec == nil {
		_, err := s.tx.Exec(ctx, `DELETE FROM account_tariffs WHERE account_id = $1`, accountID)
		return err
	}
	_, err := s.tx.Exec(ctx, `INSERT INTO account_tariffs
		(account_id, week_bitmap, timezone, valid_until, activated_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,now())
		ON CONFLICT (account_id) DO UPDATE SET
			week_bitmap=EXCLUDED.week_bitmap, timezone=EXCLUDED.timezone,
			valid_until=EXCLUDED.valid_until, activated_at=EXCLUDED.activated_at, updated_at=now()`,
		accountID, rec.WeekBitmap, rec.Timezone, rec.ValidUntil, rec.ActivatedAt)
	return err
}

func (s *sqlTx) DeleteAggregate(ctx context.Context, accountID uuid.UUID) error {
	stmts := []string{
		`DELETE FROM accessible_lessons WHERE account_id = $1`,
		`DELETE FROM lesson_completions WHERE account_id = $1`,
		`DELETE FROM progress_snapshots WHERE account_id = $1`,
		`DELETE FROM active_programs WHERE account_id = $1`,
		`DELETE FROM patient_progress WHERE account_id = $1`,
		`DELETE FROM account_tariffs WHERE account_id = $1`,
	}
	for _, q := range stmts {
		if _, err := s.tx.Exec(ctx, q, accountID); err != nil {
			return err
		}
	}
	return nil
}

func (s *sqlTx) AnonymiseAnswers(ctx context.Context, accountID uuid.UUID, replacement string, at time.Time) error {
	_, err := s.tx.Exec(ctx, `UPDATE lesson_answers SET payload = $2, anonymised = true WHERE account_id = $1`,
		accountID, replacement)
	return err
}

var (
	_ out.Repository = (*Repo)(nil)
	_ out.UnitOfWork = (*Repo)(nil)
	_ out.Tx         = (*sqlTx)(nil)
)
