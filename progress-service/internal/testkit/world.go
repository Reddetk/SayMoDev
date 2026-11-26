// Package testkit provides an in-memory world implementing the secondary ports.
// It is used by service-level and HTTP fixture tests; production code never imports it.
package testkit

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	corerr "github.com/Reddetk/SayMoDev/progress-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

type progressKey struct{ account, program uuid.UUID }
type completionKey struct{ account, lesson uuid.UUID }

// World is a transactional in-memory store. UnitOfWork serialises transactions with a mutex,
// which mirrors the row-lock serialisation used on a real aggregate.
type World struct {
	mu sync.RWMutex

	Progress       map[progressKey]out.ProgressRecord
	Programs       map[progressKey]out.ActiveProgramRecord
	Accessible     []out.AccessibleLessonRecord
	Answers        []out.AnswerRecord
	Completions    map[completionKey]out.CompletionRecord
	Snapshots      []out.SnapshotRecord
	Idempotency    map[string]bool
	Outbox         []out.OutboxEvent
	AccountTariffs map[uuid.UUID]out.TariffSnapshotRecord

	now func() time.Time
}

// NewWorld returns an empty world.
func NewWorld() *World {
	return &World{
		Progress:       map[progressKey]out.ProgressRecord{},
		Programs:       map[progressKey]out.ActiveProgramRecord{},
		Completions:    map[completionKey]out.CompletionRecord{},
		Idempotency:    map[string]bool{},
		AccountTariffs: map[uuid.UUID]out.TariffSnapshotRecord{},
		now:            func() time.Time { return time.Now().UTC() },
	}
}

// SetNow overrides the clock used by the world (tests only).
func (w *World) SetNow(fn func() time.Time) {
	w.mu.Lock()
	w.now = fn
	w.mu.Unlock()
}

func (w *World) clone() *World {
	c := &World{
		Progress:       make(map[progressKey]out.ProgressRecord, len(w.Progress)),
		Programs:       make(map[progressKey]out.ActiveProgramRecord, len(w.Programs)),
		Accessible:     append([]out.AccessibleLessonRecord(nil), w.Accessible...),
		Answers:        append([]out.AnswerRecord(nil), w.Answers...),
		Completions:    make(map[completionKey]out.CompletionRecord, len(w.Completions)),
		Snapshots:      append([]out.SnapshotRecord(nil), w.Snapshots...),
		Idempotency:    make(map[string]bool, len(w.Idempotency)),
		Outbox:         append([]out.OutboxEvent(nil), w.Outbox...),
		AccountTariffs: make(map[uuid.UUID]out.TariffSnapshotRecord, len(w.AccountTariffs)),
		now:            w.now,
	}
	for k, v := range w.Progress {
		c.Progress[k] = v
	}
	for k, v := range w.Programs {
		c.Programs[k] = v
	}
	for k, v := range w.Completions {
		c.Completions[k] = v
	}
	for k, v := range w.Idempotency {
		c.Idempotency[k] = v
	}
	for k, v := range w.AccountTariffs {
		c.AccountTariffs[k] = v
	}
	return c
}

// Do executes fn against a copy of the world and commits atomically.
func (w *World) Do(ctx context.Context, fn func(ctx context.Context, tx out.Tx) error) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	txWorld := w.clone()
	if err := fn(ctx, &tx{w: txWorld}); err != nil {
		return err
	}
	w.Progress = txWorld.Progress
	w.Programs = txWorld.Programs
	w.Accessible = txWorld.Accessible
	w.Answers = txWorld.Answers
	w.Completions = txWorld.Completions
	w.Snapshots = txWorld.Snapshots
	w.Idempotency = txWorld.Idempotency
	w.Outbox = txWorld.Outbox
	w.AccountTariffs = txWorld.AccountTariffs
	return nil
}

// --- read side ---

func (w *World) GetCurrentProgress(ctx context.Context, accountID uuid.UUID) (*out.ProgressRecord, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return currentProgress(w.Progress, accountID)
}

func (w *World) ListProgress(ctx context.Context, accountID uuid.UUID) ([]out.ProgressRecord, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return listProgress(w.Progress, accountID), nil
}

func (w *World) GetProgressByProgram(ctx context.Context, accountID, programID uuid.UUID) (*out.ProgressRecord, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if p, ok := w.Progress[progressKey{accountID, programID}]; ok {
		return copyProgress(p), nil
	}
	return nil, corerr.ErrNoActiveProgram
}

func (w *World) GetAccessibleLesson(ctx context.Context, accountID uuid.UUID) (*out.AccessibleLessonRecord, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if a, ok := activeAccessible(w.Accessible, accountID); ok {
		return &a, nil
	}
	return nil, corerr.ErrNoActiveProgram
}

func (w *World) ListAccessibleLessons(ctx context.Context, accountID uuid.UUID) ([]out.AccessibleLessonRecord, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return listAccessible(w.Accessible, accountID), nil
}

func (w *World) ListPrograms(ctx context.Context, accountID uuid.UUID) ([]out.ActiveProgramRecord, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return listPrograms(w.Programs, accountID), nil
}

func (w *World) GetActiveProgram(ctx context.Context, accountID uuid.UUID) (*out.ActiveProgramRecord, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	for _, p := range w.Programs {
		if p.AccountID == accountID && p.Status == "active" {
			cp := p
			return &cp, nil
		}
	}
	return nil, corerr.ErrNoActiveProgram
}

func (w *World) CountAnswers(ctx context.Context, accountID, lessonID uuid.UUID) (int, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	n := 0
	for _, a := range w.Answers {
		if a.AccountID == accountID && a.LessonID == lessonID {
			n++
		}
	}
	return n, nil
}

// HasCompletion reports whether a lesson was finalised.
func (w *World) HasCompletion(ctx context.Context, accountID, lessonID uuid.UUID) (bool, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	_, ok := w.Completions[completionKey{accountID, lessonID}]
	return ok, nil
}

func (w *World) ListAnswers(ctx context.Context, f out.AnswerFilter) (out.AnswerPage, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	var filtered []out.AnswerRecord
	for _, a := range w.Answers {
		if a.AccountID != f.AccountID {
			continue
		}
		if f.LessonID != nil && a.LessonID != *f.LessonID {
			continue
		}
		if f.MediaType != "" && a.MediaType != f.MediaType {
			continue
		}
		filtered = append(filtered, a)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if f.Ascending {
			return filtered[i].SubmittedAt.Before(filtered[j].SubmittedAt)
		}
		return filtered[i].SubmittedAt.After(filtered[j].SubmittedAt)
	})
	page := out.AnswerPage{Total: len(filtered)}
	start := f.Offset
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + f.Limit
	if f.Limit <= 0 || end > len(filtered) {
		end = len(filtered)
	}
	page.Answers = append([]out.AnswerRecord(nil), filtered[start:end]...)
	return page, nil
}

func (w *World) CountActivePatients(ctx context.Context, programID uuid.UUID, version int) (out.ActivePatients, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	res := out.ActivePatients{ProgramID: programID, Version: version}
	seen := map[uuid.UUID]bool{}
	for _, p := range w.Progress {
		if p.ProgramID == programID && p.ProgramVersion == version && p.Status == "active" && !seen[p.AccountID] {
			seen[p.AccountID] = true
			res.IDs = append(res.IDs, p.AccountID)
		}
	}
	res.Count = len(res.IDs)
	return res, nil
}

func (w *World) GetAccountTariff(ctx context.Context, accountID uuid.UUID) (*out.TariffSnapshotRecord, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if t, ok := w.AccountTariffs[accountID]; ok {
		cp := t
		return &cp, nil
	}
	return nil, nil
}

func (w *World) ListProgressByProgram(ctx context.Context, programID uuid.UUID) ([]out.ProgressRecord, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	var res []out.ProgressRecord
	for _, p := range w.Progress {
		if p.ProgramID == programID {
			res = append(res, *copyProgress(p))
		}
	}
	return res, nil
}

func (w *World) FetchUnsentOutbox(ctx context.Context, limit int) ([]out.OutboxEvent, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	var res []out.OutboxEvent
	for _, e := range w.Outbox {
		if e.SentAt == nil {
			res = append(res, e)
		}
		if limit > 0 && len(res) >= limit {
			break
		}
	}
	return res, nil
}

func (w *World) MarkOutboxSent(ctx context.Context, ids []uuid.UUID) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := range w.Outbox {
		for _, id := range ids {
			if w.Outbox[i].ID == id {
				t := w.now()
				w.Outbox[i].SentAt = &t
			}
		}
	}
	return nil
}

func (w *World) Ping(context.Context) error { return nil }

// ActiveProgramCount implements the gauge projection.
func (w *World) ActiveProgramCount(ctx context.Context) (int, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	n := 0
	for _, p := range w.Programs {
		if p.Status == "active" {
			n++
		}
	}
	return n, nil
}

// --- helpers ---

func copyProgress(p out.ProgressRecord) *out.ProgressRecord {
	cp := p
	if p.Tariff != nil {
		t := *p.Tariff
		cp.Tariff = &t
	}
	return &cp
}

func currentProgress(m map[progressKey]out.ProgressRecord, accountID uuid.UUID) (*out.ProgressRecord, error) {
	var rows []out.ProgressRecord
	for _, p := range m {
		if p.AccountID == accountID {
			rows = append(rows, p)
		}
	}
	if len(rows) == 0 {
		return nil, corerr.ErrNoActiveProgram
	}
	rank := func(s string) int {
		switch s {
		case "active":
			return 0
		case "completed":
			return 1
		default:
			return 2
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rank(rows[i].Status) != rank(rows[j].Status) {
			return rank(rows[i].Status) < rank(rows[j].Status)
		}
		return rows[i].LastUpdatedAt.After(rows[j].LastUpdatedAt)
	})
	return copyProgress(rows[0]), nil
}

func listProgress(m map[progressKey]out.ProgressRecord, accountID uuid.UUID) []out.ProgressRecord {
	var res []out.ProgressRecord
	for _, p := range m {
		if p.AccountID == accountID {
			res = append(res, *copyProgress(p))
		}
	}
	sort.Slice(res, func(i, j int) bool { return res[i].LastUpdatedAt.After(res[j].LastUpdatedAt) })
	return res
}

func activeAccessible(all []out.AccessibleLessonRecord, accountID uuid.UUID) (out.AccessibleLessonRecord, bool) {
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].AccountID == accountID && all[i].RevokedAt == nil {
			return all[i], true
		}
	}
	return out.AccessibleLessonRecord{}, false
}

func listAccessible(all []out.AccessibleLessonRecord, accountID uuid.UUID) []out.AccessibleLessonRecord {
	var res []out.AccessibleLessonRecord
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].AccountID == accountID {
			res = append(res, all[i])
		}
	}
	return res
}

func listPrograms(m map[progressKey]out.ActiveProgramRecord, accountID uuid.UUID) []out.ActiveProgramRecord {
	var res []out.ActiveProgramRecord
	for _, p := range m {
		if p.AccountID == accountID {
			res = append(res, p)
		}
	}
	sort.Slice(res, func(i, j int) bool { return res[i].ActivatedAt.After(res[j].ActivatedAt) })
	return res
}

// ErrDuplicate signals an unexpected duplicate in test setup.
var ErrDuplicate = errors.New("duplicate")
