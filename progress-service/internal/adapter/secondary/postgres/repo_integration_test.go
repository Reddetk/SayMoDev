//go:build integration

package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corerr "github.com/Reddetk/SayMoDev/progress-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

// Run with: POSTGRES_DSN=... go test -tags=integration ./internal/adapter/secondary/postgres -v
func testRepo(t *testing.T) *Repo {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not set")
	}
	repo, err := New(context.Background(), dsn, 4, 1, time.Minute)
	require.NoError(t, err)
	t.Cleanup(repo.Close)
	return repo
}

func TestRepositoryInvariants(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()
	account := uuid.New()
	program := uuid.New()
	t.Cleanup(func() { cleanup(ctx, repo, account) })

	now := time.Now().UTC()
	require.NoError(t, repo.Do(ctx, func(ctx context.Context, tx out.Tx) error {
		if err := tx.InsertActiveProgram(ctx, out.ActiveProgramRecord{
			AccountID: account, ProgramID: program, Version: 1, Status: "active",
			Difficulty: "easy", AphasiaType: "motor_aphasia", ActivatedAt: now,
		}); err != nil {
			return err
		}
		// Upsert on the second insert (difficulty switch back) must not fail.
		if err := tx.InsertActiveProgram(ctx, out.ActiveProgramRecord{
			AccountID: account, ProgramID: program, Version: 1, Status: "active",
			Difficulty: "easy", AphasiaType: "motor_aphasia", ActivatedAt: now,
		}); err != nil {
			return err
		}
		return tx.InsertProgress(ctx, out.ProgressRecord{
			AccountID: account, ProgramID: program, ProgramVersion: 1, Status: "active",
			TotalLessons: 3, CompletionPercent: 0, LastUpdatedAt: now,
			Tariff: &out.TariffSnapshotRecord{WeekBitmap: 127, Timezone: "UTC", ActivatedAt: now},
		})
	}))

	// A second active program for the same account violates the partial unique index.
	err := repo.Do(ctx, func(ctx context.Context, tx out.Tx) error {
		return tx.InsertActiveProgram(ctx, out.ActiveProgramRecord{
			AccountID: account, ProgramID: uuid.New(), Version: 1, Status: "active",
			Difficulty: "hard", AphasiaType: "motor_aphasia", ActivatedAt: now,
		})
	})
	assert.ErrorIs(t, err, corerr.ErrProgramAlreadyActive)

	require.NoError(t, repo.Do(ctx, func(ctx context.Context, tx out.Tx) error {
		return tx.InsertAccessibleLesson(ctx, out.AccessibleLessonRecord{
			AccountID: account, LessonID: uuid.New(), ProgramID: program,
			LessonType: "regular", OrderPosition: 1, GrantedAt: now,
		})
	}))
	second := repo.Do(ctx, func(ctx context.Context, tx out.Tx) error {
		return tx.InsertAccessibleLesson(ctx, out.AccessibleLessonRecord{
			AccountID: account, LessonID: uuid.New(), ProgramID: program,
			LessonType: "regular", OrderPosition: 2, GrantedAt: now,
		})
	})
	assert.ErrorIs(t, second, corerr.ErrLessonNotAccessible, "only one granted lesson is allowed")

	// Answers are append-only and counted.
	require.NoError(t, repo.Do(ctx, func(ctx context.Context, tx out.Tx) error {
		for i := 0; i < 2; i++ {
			if err := tx.InsertAnswer(ctx, out.AnswerRecord{
				AnswerID: uuid.New(), AccountID: account, LessonID: uuid.New(), ProgramID: program,
				MediaType: "text", Payload: "answer", SubmittedAt: now.Add(time.Duration(i) * time.Second),
			}); err != nil {
				return err
			}
		}
		return nil
	}))
	page, err := repo.ListAnswers(ctx, out.AnswerFilter{AccountID: account, Limit: 50, Ascending: true})
	require.NoError(t, err)
	assert.Equal(t, 2, page.Total)

	stats, err := repo.CountActivePatients(ctx, program, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Count)
	assert.Contains(t, stats.IDs, account)

	// GDPR delete removes aggregate rows but keeps anonymised answers.
	require.NoError(t, repo.Do(ctx, func(ctx context.Context, tx out.Tx) error {
		if err := tx.AnonymiseAnswers(ctx, account, "[deleted by user]", now); err != nil {
			return err
		}
		return tx.DeleteAggregate(ctx, account)
	}))
	_, err = repo.GetCurrentProgress(ctx, account)
	assert.ErrorIs(t, err, corerr.ErrNoActiveProgram)
	page, err = repo.ListAnswers(ctx, out.AnswerFilter{AccountID: account, Limit: 50})
	require.NoError(t, err)
	assert.Equal(t, 2, page.Total, "medical answer rows are retained")
	assert.Equal(t, "[deleted by user]", page.Answers[0].Payload)
}

func cleanup(ctx context.Context, repo *Repo, account uuid.UUID) {
	_ = repo.Do(ctx, func(ctx context.Context, tx out.Tx) error { return tx.DeleteAggregate(ctx, account) })
}
