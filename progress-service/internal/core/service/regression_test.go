package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	corerr "github.com/Reddetk/SayMoDev/progress-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
	"github.com/Reddetk/SayMoDev/progress-service/internal/testkit"
)

type fixture struct {
	world    *testkit.World
	cat      *testkit.Catalog
	store    *testkit.Storage
	progress *ProgressService
	lessons  *LessonService
	admin    *AdminService
	events   *EventService
}

func newFixture() *fixture {
	w := testkit.NewWorld()
	c := testkit.NewCatalog()
	store := &testkit.Storage{}
	log := zap.NewNop()
	return &fixture{
		world: w, cat: c, store: store,
		progress: NewProgressService(w, w, c, nil, log, time.Minute, 127),
		lessons:  NewLessonService(w, w, c, store, nil, log, time.Minute, 15*time.Minute),
		admin:    NewAdminService(w, w, c, store, NewProgressService(w, w, c, nil, log, time.Minute, 127), log, time.Hour),
		events:   NewEventService(w, w, c, nil, log, time.Minute, 127),
	}
}

func (f *fixture) activate(t *testing.T, account uuid.UUID, difficulty string) {
	t.Helper()
	_, err := f.progress.Activate(context.Background(), in.ActivateCommand{
		AccountID: account, HasClassifier: true, Difficulty: difficulty, AphasiaType: "motor_aphasia",
	})
	require.NoError(t, err)
}

func (f *fixture) currentLesson(t *testing.T, account uuid.UUID) uuid.UUID {
	t.Helper()
	view, err := f.progress.GetMyProgress(context.Background(), account)
	require.NoError(t, err)
	require.NotNil(t, view.AccessibleLesson)
	return uuid.MustParse(view.AccessibleLesson.LessonID)
}

func (f *fixture) answerAndComplete(t *testing.T, account, lesson uuid.UUID) in.CompleteResult {
	t.Helper()
	_, err := f.lessons.Submit(context.Background(), in.SubmitAnswerCommand{
		AccountID: account, LessonID: lesson, MediaType: "text", Payload: "answer",
	})
	require.NoError(t, err)
	res, err := f.lessons.Complete(context.Background(), in.CompleteCommand{AccountID: account, LessonID: lesson})
	require.NoError(t, err)
	return res
}

// TestSwitchBackABAVerifiesUpsert covers easyв†’mediumв†’easy (previously a PK conflict / 500).
func TestSwitchBackABAVerifiesUpsert(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	account := uuid.New()

	f.activate(t, account, "easy")
	if _, err := f.progress.ChangeDifficulty(ctx, in.ChangeDifficultyCommand{AccountID: account, NewDifficulty: "medium"}); err != nil {
		t.Fatalf("easyв†’medium: %v", err)
	}
	res, err := f.progress.ChangeDifficulty(ctx, in.ChangeDifficultyCommand{AccountID: account, NewDifficulty: "easy"})
	require.NoError(t, err, "mediumв†’easy must not hit a duplicate key")
	assert.Equal(t, testkit.ProgramID("easy").String(), res.NewProgramID)

	view, err := f.progress.GetMyProgress(ctx, account)
	require.NoError(t, err)
	assert.Equal(t, testkit.ProgramID("easy").String(), view.ProgramID)
	assert.Equal(t, 0, view.CompletionPercent)

	programs, err := f.world.ListPrograms(ctx, account)
	require.NoError(t, err)
	active, paused := 0, 0
	for _, p := range programs {
		switch p.Status {
		case "active":
			active++
		case "paused":
			paused++
		}
	}
	assert.Equal(t, 1, active)
	assert.Equal(t, 1, paused)
}

// TestParallelSubmitAndComplete asserts no accepted answer is timestamped after completion.
func TestParallelSubmitAndComplete(t *testing.T) {
	for i := 0; i < 40; i++ {
		f := newFixture()
		ctx := context.Background()
		account := uuid.New()
		f.activate(t, account, "easy")
		lesson := f.currentLesson(t, account)

		// One answer exists so completion is eligible.
		_, err := f.lessons.Submit(ctx, in.SubmitAnswerCommand{AccountID: account, LessonID: lesson, MediaType: "text", Payload: "first"})
		require.NoError(t, err)

		var wg sync.WaitGroup
		start := make(chan struct{})
		var submitRes in.SubmitAnswerResult
		var submitErr, completeErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			submitRes, submitErr = f.lessons.Submit(ctx, in.SubmitAnswerCommand{
				AccountID: account, LessonID: lesson, MediaType: "text", Payload: "racer",
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, completeErr = f.lessons.Complete(ctx, in.CompleteCommand{AccountID: account, LessonID: lesson})
		}()
		close(start)
		wg.Wait()

		if submitErr == nil {
			progress, err := f.world.GetCurrentProgress(ctx, account)
			require.NoError(t, err)
			if progress.LastCompletedAt != nil {
				assert.False(t, submitRes.SubmittedAt.After(*progress.LastCompletedAt),
					"accepted answer must not be timestamped after completion")
			}
			if completeErr != nil {
				// Completion lost the race (e.g. no answer at its check time): answer is legal.
				assert.ErrorIs(t, completeErr, corerr.ErrNoAnswerSubmitted)
			}
		}
		if submitErr != nil && completeErr != nil {
			t.Fatalf("both racing operations failed: submit=%v complete=%v", submitErr, completeErr)
		}
	}
}

// TestDeleteEmitsProgramCompletedOnlyForActiveProgram covers the BC#3 double-decrement finding.
func TestDeleteEmitsProgramCompletedOnlyForActiveProgram(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	account := uuid.New()

	// easy в†’ paused, medium completed: no active program at delete time.
	f.activate(t, account, "easy")
	if _, err := f.progress.ChangeDifficulty(ctx, in.ChangeDifficultyCommand{AccountID: account, NewDifficulty: "medium"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		f.answerAndComplete(t, account, f.currentLesson(t, account))
	}

	before := len(f.world.SortedOutbox())
	require.NoError(t, f.progress.DeleteMyProgress(ctx, in.DeleteCommand{
		AccountID: account, Confirm: "delete-my-progress",
	}))

	events := f.world.SortedOutbox()[before:]
	assert.Equal(t, 0, countEvents(events, "ProgramCompleted"),
		"completed/paused programs must not decrement BC#3 again")
	assert.Equal(t, 1, countEvents(events, "ProgressDeleted"))

	// A separate account with an ACTIVE program emits exactly one ProgramCompleted.
	active := uuid.New()
	f.activate(t, active, "hard")
	before = len(f.world.SortedOutbox())
	require.NoError(t, f.progress.DeleteMyProgress(ctx, in.DeleteCommand{AccountID: active, Confirm: "delete-my-progress"}))
	events = f.world.SortedOutbox()[before:]
	assert.Equal(t, 1, countEventsForAccount(events, "ProgramCompleted", active))
}

// TestAccountDeletedEventIsIdempotent covers the BC#1 AccountDeleted consumer path.
func TestAccountDeletedEventIsIdempotent(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	account := uuid.New()
	f.activate(t, account, "easy")
	_, err := f.lessons.Submit(ctx, in.SubmitAnswerCommand{
		AccountID: account, LessonID: f.currentLesson(t, account), MediaType: "text", Payload: "medical note",
	})
	require.NoError(t, err)

	ev := in.AccountDeletedEvent{EventID: "deleted-1", AccountID: account}
	require.NoError(t, f.events.OnAccountDeleted(ctx, ev))
	require.NoError(t, f.events.OnAccountDeleted(ctx, ev), "duplicate event is a no-op")

	answers, err := f.world.ListAnswers(ctx, out.AnswerFilter{AccountID: account, Limit: 50})
	require.NoError(t, err)
	require.Equal(t, 1, answers.Total)
	assert.Equal(t, "[deleted by user]", answers.Answers[0].Payload)

	_, err = f.progress.GetMyProgress(ctx, account)
	assert.ErrorIs(t, err, corerr.ErrNoActiveProgram)
	assert.Equal(t, 1, countEvents(f.world.SortedOutbox(), "ProgressDeleted"))
}

// TestTariffBeforeRegistrationIsApplied covers event ordering (BC#2 before BC#1).
func TestTariffBeforeRegistrationIsApplied(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	account := uuid.New()

	err := f.events.OnTariffChanged(ctx, in.TariffEvent{
		EventID: "tariff-1", AccountID: account, EventType: "TariffPlanActivated",
		WeekBitmap: 0b0000010, Timezone: "Europe/Moscow", HasSnapshot: true,
	})
	require.NoError(t, err)

	err = f.events.OnAccountRegistered(ctx, in.RegistrationEvent{
		EventID: "reg-1", AccountID: account.String(), Role: "patient",
		Difficulty: "easy", AphasiaType: "motor_aphasia", HasClassifier: true,
	})
	require.NoError(t, err)

	view, err := f.progress.GetMyProgress(ctx, account)
	require.NoError(t, err)
	require.NotNil(t, view.TariffSnapshot)
	assert.Equal(t, 0b0000010, view.TariffSnapshot.WeekBitmap)
	assert.Equal(t, "Europe/Moscow", view.TariffSnapshot.Timezone)

	// Duplicate registration is idempotent.
	require.NoError(t, f.events.OnAccountRegistered(ctx, in.RegistrationEvent{
		EventID: "reg-1", AccountID: account.String(), Role: "patient",
		Difficulty: "easy", AphasiaType: "motor_aphasia", HasClassifier: true,
	}))
	programs, err := f.world.ListPrograms(ctx, account)
	require.NoError(t, err)
	assert.Len(t, programs, 1)
}

func countEvents(events []out.OutboxEvent, eventType string) int {
	n := 0
	for _, e := range events {
		if e.EventType == eventType {
			n++
		}
	}
	return n
}

func countEventsForAccount(events []out.OutboxEvent, eventType string, account uuid.UUID) int {
	n := 0
	for _, e := range events {
		if e.EventType != eventType {
			continue
		}
		var payload struct {
			AccountID  string `json:"accountId"`
			AccountID2 string `json:"account_id"`
		}
		_ = json.Unmarshal(e.Payload, &payload)
		if payload.AccountID == account.String() || payload.AccountID2 == account.String() {
			n++
		}
	}
	return n
}
