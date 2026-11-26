package testkit

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"

	corerr "github.com/Reddetk/SayMoDev/progress-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

type tx struct {
	w *World
}

func (t *tx) GetProgressForUpdate(ctx context.Context, accountID uuid.UUID) (*out.ProgressRecord, error) {
	return currentProgress(t.w.Progress, accountID)
}

func (t *tx) GetProgressByProgramForUpdate(ctx context.Context, accountID, programID uuid.UUID) (*out.ProgressRecord, error) {
	if p, ok := t.w.Progress[progressKey{accountID, programID}]; ok {
		return copyProgress(p), nil
	}
	return nil, corerr.ErrNoActiveProgram
}

func (t *tx) ListProgress(ctx context.Context, accountID uuid.UUID) ([]out.ProgressRecord, error) {
	return listProgress(t.w.Progress, accountID), nil
}

func (t *tx) ListProgressByProgramForUpdate(ctx context.Context, programID uuid.UUID) ([]out.ProgressRecord, error) {
	var res []out.ProgressRecord
	for _, p := range t.w.Progress {
		if p.ProgramID == programID {
			res = append(res, *copyProgress(p))
		}
	}
	return res, nil
}

func (t *tx) InsertProgress(ctx context.Context, rec out.ProgressRecord) error {
	t.w.Progress[progressKey{rec.AccountID, rec.ProgramID}] = *copyProgress(rec)
	return nil
}

func (t *tx) UpdateProgress(ctx context.Context, rec out.ProgressRecord) error {
	t.w.Progress[progressKey{rec.AccountID, rec.ProgramID}] = *copyProgress(rec)
	return nil
}

func (t *tx) GetActiveProgramForUpdate(ctx context.Context, accountID uuid.UUID) (*out.ActiveProgramRecord, error) {
	for _, p := range t.w.Programs {
		if p.AccountID == accountID && p.Status == "active" {
			cp := p
			return &cp, nil
		}
	}
	return nil, corerr.ErrNoActiveProgram
}

func (t *tx) InsertActiveProgram(ctx context.Context, rec out.ActiveProgramRecord) error {
	if rec.Status == "active" {
		for _, p := range t.w.Programs {
			if p.AccountID == rec.AccountID && p.Status == "active" && p.ProgramID != rec.ProgramID {
				return corerr.ErrProgramAlreadyActive
			}
		}
	}
	t.w.Programs[progressKey{rec.AccountID, rec.ProgramID}] = rec
	return nil
}

func (t *tx) UpdateActiveProgram(ctx context.Context, rec out.ActiveProgramRecord) error {
	if rec.Status == "active" {
		for _, p := range t.w.Programs {
			if p.AccountID == rec.AccountID && p.Status == "active" && p.ProgramID != rec.ProgramID {
				return corerr.ErrProgramAlreadyActive
			}
		}
	}
	t.w.Programs[progressKey{rec.AccountID, rec.ProgramID}] = rec
	return nil
}

func (t *tx) ListPrograms(ctx context.Context, accountID uuid.UUID) ([]out.ActiveProgramRecord, error) {
	return listPrograms(t.w.Programs, accountID), nil
}

func (t *tx) GetAccessibleLessonForUpdate(ctx context.Context, accountID uuid.UUID) (*out.AccessibleLessonRecord, error) {
	if a, ok := activeAccessible(t.w.Accessible, accountID); ok {
		return &a, nil
	}
	return nil, corerr.ErrNoActiveProgram
}

func (t *tx) InsertAccessibleLesson(ctx context.Context, rec out.AccessibleLessonRecord) error {
	if _, ok := activeAccessible(t.w.Accessible, rec.AccountID); ok {
		return corerr.ErrLessonNotAccessible
	}
	t.w.Accessible = append(t.w.Accessible, rec)
	return nil
}

func (t *tx) RevokeAccessibleLessons(ctx context.Context, accountID uuid.UUID, at time.Time) (int64, error) {
	var n int64
	for i := range t.w.Accessible {
		if t.w.Accessible[i].AccountID == accountID && t.w.Accessible[i].RevokedAt == nil {
			revoked := at
			t.w.Accessible[i].RevokedAt = &revoked
			n++
		}
	}
	return n, nil
}

func (t *tx) HasCompletion(ctx context.Context, accountID, lessonID uuid.UUID) (bool, error) {
	_, ok := t.w.Completions[completionKey{accountID, lessonID}]
	return ok, nil
}

func (t *tx) CountAnswers(ctx context.Context, accountID, lessonID uuid.UUID) (int, error) {
	n := 0
	for _, a := range t.w.Answers {
		if a.AccountID == accountID && a.LessonID == lessonID {
			n++
		}
	}
	return n, nil
}

func (t *tx) InsertCompletion(ctx context.Context, rec out.CompletionRecord) error {
	k := completionKey{rec.AccountID, rec.LessonID}
	if _, ok := t.w.Completions[k]; ok {
		return corerr.ErrLessonAlreadyCompleted
	}
	t.w.Completions[k] = rec
	return nil
}

func (t *tx) InsertAnswer(ctx context.Context, rec out.AnswerRecord) error {
	t.w.Answers = append(t.w.Answers, rec)
	return nil
}

func (t *tx) InsertSnapshot(ctx context.Context, rec out.SnapshotRecord) error {
	t.w.Snapshots = append(t.w.Snapshots, rec)
	return nil
}

func (t *tx) InsertOutbox(ctx context.Context, ev out.OutboxEvent) error {
	t.w.Outbox = append(t.w.Outbox, ev)
	return nil
}

func (t *tx) RecordIdempotency(ctx context.Context, rec out.IdempotencyRecord) (bool, error) {
	if t.w.Idempotency[rec.EventID] {
		return false, nil
	}
	t.w.Idempotency[rec.EventID] = true
	return true, nil
}

func (t *tx) GetAccountTariff(ctx context.Context, accountID uuid.UUID) (*out.TariffSnapshotRecord, error) {
	if v, ok := t.w.AccountTariffs[accountID]; ok {
		cp := v
		return &cp, nil
	}
	return nil, nil
}

func (t *tx) UpsertAccountTariff(ctx context.Context, accountID uuid.UUID, rec *out.TariffSnapshotRecord) error {
	if rec == nil {
		delete(t.w.AccountTariffs, accountID)
		return nil
	}
	t.w.AccountTariffs[accountID] = *rec
	return nil
}

func (t *tx) DeleteAggregate(ctx context.Context, accountID uuid.UUID) error {
	for k := range t.w.Progress {
		if k.account == accountID {
			delete(t.w.Progress, k)
		}
	}
	for k := range t.w.Programs {
		if k.account == accountID {
			delete(t.w.Programs, k)
		}
	}
	for k := range t.w.Completions {
		if k.account == accountID {
			delete(t.w.Completions, k)
		}
	}
	var acc []out.AccessibleLessonRecord
	for _, a := range t.w.Accessible {
		if a.AccountID != accountID {
			acc = append(acc, a)
		}
	}
	t.w.Accessible = acc
	var snaps []out.SnapshotRecord
	for _, s := range t.w.Snapshots {
		if s.AccountID != accountID {
			snaps = append(snaps, s)
		}
	}
	t.w.Snapshots = snaps
	delete(t.w.AccountTariffs, accountID)
	return nil
}

func (t *tx) AnonymiseAnswers(ctx context.Context, accountID uuid.UUID, replacement string, at time.Time) error {
	for i := range t.w.Answers {
		if t.w.Answers[i].AccountID == accountID {
			t.w.Answers[i].Payload = replacement
			t.w.Answers[i].Anonymised = true
		}
	}
	return nil
}

// SortedOutbox is a test helper: outbox events in insertion order.
func (w *World) SortedOutbox() []out.OutboxEvent {
	w.mu.RLock()
	defer w.mu.RUnlock()
	res := append([]out.OutboxEvent(nil), w.Outbox...)
	sort.SliceStable(res, func(i, j int) bool { return res[i].OccurredAt.Before(res[j].OccurredAt) })
	return res
}

var (
	_ out.Repository = (*World)(nil)
	_ out.UnitOfWork = (*World)(nil)
	_ out.Tx         = (*tx)(nil)
)
