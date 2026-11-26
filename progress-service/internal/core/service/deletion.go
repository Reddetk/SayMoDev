package service

import (
	"context"
	"time"

	"github.com/google/uuid"

	corerr "github.com/Reddetk/SayMoDev/progress-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/progress-service/internal/core/valobj"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

// deleteAccountData implements GDPR self-delete and the AccountDeleted consumer path.
// Answers are anonymised (rows retained), aggregate data is removed, and BC#3 is notified
// exactly once per ACTIVE program (completed programs already decremented at 100%).
func deleteAccountData(ctx context.Context, tx out.Tx, accountID uuid.UUID, now time.Time) error {
	programs, err := tx.ListPrograms(ctx, accountID)
	if err != nil {
		return err
	}
	if len(programs) == 0 {
		return corerr.ErrNoActiveProgram
	}

	for _, p := range programs {
		if p.Status != string(valobj.ProgramStatusActive) {
			continue
		}
		ev, err := newOutbox(ctx, "ProgramCompleted", map[string]any{
			"accountId": accountID.String(), "programId": p.ProgramID.String(),
			"completionPercent": p.CompletionPercent, "completedAt": now.Format(time.RFC3339),
		}, now)
		if err != nil {
			return err
		}
		if err := tx.InsertOutbox(ctx, ev); err != nil {
			return err
		}
	}

	if err := tx.AnonymiseAnswers(ctx, accountID, "[deleted by user]", now); err != nil {
		return err
	}
	if err := tx.DeleteAggregate(ctx, accountID); err != nil {
		return err
	}
	ev, err := newOutbox(ctx, "ProgressDeleted", map[string]any{
		"accountId": accountID.String(), "deletedAt": now.Format(time.RFC3339),
	}, now)
	if err != nil {
		return err
	}
	return tx.InsertOutbox(ctx, ev)
}
