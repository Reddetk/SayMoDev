package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	corerr "github.com/Reddetk/SayMoDev/progress-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/progress-service/internal/core/valobj"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

// activateInTx creates the active program, progress row, first accessible lesson and outbox event.
// Callers own the transaction and the idempotency key (HTTP path is guarded by the idempotency middleware,
// Kafka path by tx.RecordIdempotency).
func activateInTx(ctx context.Context, tx out.Tx, accountID uuid.UUID, prog out.ProgramInfo, difficulty, aphasia string, defaultBitmap int, now time.Time) (out.LessonInfo, error) {
	existing, err := tx.GetActiveProgramForUpdate(ctx, accountID)
	if err != nil && !errors.Is(err, corerr.ErrNoActiveProgram) {
		return out.LessonInfo{}, err
	}
	if existing != nil {
		return out.LessonInfo{}, corerr.ErrProgramAlreadyActive
	}

	first, ok := firstLesson(prog)
	if !ok {
		return out.LessonInfo{}, corerr.ErrProgramCatalogUnavailable
	}

	current, err := tx.GetProgressForUpdate(ctx, accountID)
	if err != nil && !errors.Is(err, corerr.ErrNoActiveProgram) {
		return out.LessonInfo{}, err
	}
	var tariff *valobj.TariffSnapshot
	if current != nil && current.Tariff != nil {
		tariff = toProgressEntity(*current).Tariff
	}
	if tariff == nil {
		if pending, err := tx.GetAccountTariff(ctx, accountID); err != nil {
			return out.LessonInfo{}, err
		} else if pending != nil {
			tariff = &valobj.TariffSnapshot{
				WeekBitmap: pending.WeekBitmap, Timezone: pending.Timezone,
				ValidUntil: pending.ValidUntil, ActivatedAt: pending.ActivatedAt,
			}
		}
	}
	if tariff == nil {
		// Documented catalogue default; nil in prod until a real tariff event arrives.
		tariff = defaultTariff(now, defaultBitmap)
	}

	if difficulty == "" {
		difficulty = prog.Difficulty
	}
	if aphasia == "" {
		aphasia = prog.AphasiaType
	}

	ap := out.ActiveProgramRecord{
		AccountID:   accountID,
		ProgramID:   prog.ProgramID,
		Version:     prog.Version,
		Status:      string(valobj.ProgramStatusActive),
		Difficulty:  difficulty,
		AphasiaType: aphasia,
		ActivatedAt: now,
	}
	if err := tx.InsertActiveProgram(ctx, ap); err != nil {
		return out.LessonInfo{}, err
	}

	pr := out.ProgressRecord{
		AccountID:         accountID,
		ProgramID:         prog.ProgramID,
		ProgramVersion:    prog.Version,
		Status:            string(valobj.ProgramStatusActive),
		TotalLessons:      prog.TotalLessons,
		CompletionPercent: 0,
		LastUpdatedAt:     now,
	}
	if tariff != nil {
		pr.Tariff = &out.TariffSnapshotRecord{
			WeekBitmap: tariff.WeekBitmap, Timezone: tariff.Timezone,
			ValidUntil: tariff.ValidUntil, ActivatedAt: tariff.ActivatedAt,
		}
	}
	if err := tx.InsertProgress(ctx, pr); err != nil {
		return out.LessonInfo{}, err
	}
	if tariff != nil {
		if err := tx.UpsertAccountTariff(ctx, accountID, &out.TariffSnapshotRecord{
			WeekBitmap: tariff.WeekBitmap, Timezone: tariff.Timezone,
			ValidUntil: tariff.ValidUntil, ActivatedAt: tariff.ActivatedAt,
		}); err != nil {
			return out.LessonInfo{}, err
		}
	}
	if err := tx.InsertAccessibleLesson(ctx, out.AccessibleLessonRecord{
		AccountID: accountID, LessonID: first.LessonID, ProgramID: prog.ProgramID,
		LessonType: first.LessonType, OrderPosition: first.OrderPosition, GrantedAt: now,
	}); err != nil {
		return out.LessonInfo{}, err
	}

	ev, err := newOutbox(ctx, "ProgramActivated", map[string]any{
		"accountId":   accountID.String(),
		"programId":   prog.ProgramID.String(),
		"version":     prog.Version,
		"activatedAt": now.Format(time.RFC3339),
	}, now)
	if err != nil {
		return out.LessonInfo{}, err
	}
	if err := tx.InsertOutbox(ctx, ev); err != nil {
		return out.LessonInfo{}, err
	}
	return first, nil
}
