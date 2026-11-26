package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	corerr "github.com/Reddetk/SayMoDev/progress-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/progress-service/internal/core/valobj"
	"github.com/Reddetk/SayMoDev/progress-service/internal/logger"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

const idempotencyTTL = 7 * 24 * time.Hour

// EventService implements the Kafka-driven use cases (T2 consumer template).
type EventService struct {
	repo    out.Repository
	uow     out.UnitOfWork
	catalog out.ContentCatalog
	cache   cacheRec
	log     logger.Logger
	now     func() time.Time

	defaultBitmap int
}

// NewEventService wires consumer-side use cases.
func NewEventService(repo out.Repository, uow out.UnitOfWork, catalog out.ContentCatalog, cache out.Cache, log logger.Logger, cacheTTL time.Duration, defaultBitmap int) *EventService {
	return &EventService{
		repo: repo, uow: uow, catalog: catalog,
		cache:         cacheRec{cache: cache, log: log, localTTL: cacheTTL},
		log:           log,
		now:           func() time.Time { return time.Now().UTC() },
		defaultBitmap: defaultBitmap,
	}
}

// OnAccountRegistered auto-activates the initial program from the event classifier.
func (s *EventService) OnAccountRegistered(ctx context.Context, ev in.RegistrationEvent) error {
	if ev.Role != "" && ev.Role != "patient" {
		s.log.Warn("account_registered_ignored_role", zap.String("role", ev.Role), zap.String("event_id", ev.EventID))
		return nil
	}
	if !ev.HasClassifier || ev.Difficulty == "" {
		return fmt.Errorf("account_registered %s: classifier missing (DLQ)", ev.EventID)
	}
	accountID, err := uuid.Parse(ev.AccountID)
	if err != nil {
		return fmt.Errorf("account_registered %s: invalid accountId: %w", ev.EventID, err)
	}

	prog, err := s.catalog.ResolveProgram(ctx, ev.Difficulty, ev.AphasiaType)
	if err != nil {
		if errors.Is(err, corerr.ErrNoProgramForClassifier) || errors.Is(err, corerr.ErrProgramNotFound) {
			s.log.Warn("no_program_for_classifier",
				zap.String("event_id", ev.EventID), zap.String("difficulty", ev.Difficulty), zap.String("aphasia_type", ev.AphasiaType))
			return nil
		}
		return err
	}

	err = s.uow.Do(ctx, func(ctx context.Context, tx out.Tx) error {
		now := s.now()
		fresh, err := tx.RecordIdempotency(ctx, out.IdempotencyRecord{EventID: ev.EventID, ExpiresAt: now.Add(idempotencyTTL)})
		if err != nil {
			return err
		}
		if !fresh {
			return nil
		}
		_, err = activateInTx(ctx, tx, accountID, prog, ev.Difficulty, ev.AphasiaType, s.defaultBitmap, now)
		if errors.Is(err, corerr.ErrProgramAlreadyActive) {
			s.log.Info("program_activation_skipped_already_active", zap.String("account_id", accountID.String()))
			return nil
		}
		return err
	})
	if err != nil {
		return err
	}
	s.invalidate(ctx, accountID)
	return nil
}

// OnAccountDeleted anonymises PII and removes progress after BC#1 account deletion.
func (s *EventService) OnAccountDeleted(ctx context.Context, ev in.AccountDeletedEvent) error {
	err := s.uow.Do(ctx, func(ctx context.Context, tx out.Tx) error {
		now := s.now()
		fresh, err := tx.RecordIdempotency(ctx, out.IdempotencyRecord{EventID: ev.EventID, ExpiresAt: now.Add(idempotencyTTL)})
		if err != nil {
			return err
		}
		if !fresh {
			return nil
		}
		err = deleteAccountData(ctx, tx, ev.AccountID, now)
		if errors.Is(err, corerr.ErrNoActiveProgram) {
			return nil // already absent: idempotent
		}
		return err
	})
	if err != nil {
		return err
	}
	s.invalidate(ctx, ev.AccountID)
	s.log.Info("progress_deleted_event", zap.String("account_id", ev.AccountID.String()))
	return nil
}

// OnTariffChanged applies BC#2 tariff/subscription events to the local TariffSnapshot and lesson access.
func (s *EventService) OnTariffChanged(ctx context.Context, ev in.TariffEvent) error {
	ctx, span := tracer.Start(ctx, "progress.recalc_access.total")
	defer span.End()
	grant, revoke, clear, _ := tariffEffect(ev.EventType)
	if !grant && !revoke && !clear {
		// SubscriptionDeActivated: tariff lives until period_end, no immediate action.
		return nil
	}

	progress, err := s.repo.GetCurrentProgress(ctx, ev.AccountID)
	if err != nil && !errors.Is(err, corerr.ErrNoActiveProgram) {
		return err
	}
	if errors.Is(err, corerr.ErrNoActiveProgram) {
		progress = nil // tariff arrived before activation: store it for AccountRegistered
	}

	var prog out.ProgramInfo
	if grant && progress != nil {
		prog, err = s.catalog.GetProgram(ctx, progress.ProgramID.String(), progress.ProgramVersion)
		if err != nil {
			return err
		}
	}

	err = s.uow.Do(ctx, func(ctx context.Context, tx out.Tx) error {
		now := s.now()
		fresh, err := tx.RecordIdempotency(ctx, out.IdempotencyRecord{EventID: ev.EventID, ExpiresAt: now.Add(idempotencyTTL)})
		if err != nil {
			return err
		}
		if !fresh {
			return nil
		}

		// Account-level snapshot survives activation order: if the tariff comes first,
		// AccountRegistered reads it through account_tariffs instead of using defaults.
		switch {
		case clear:
			if err := tx.UpsertAccountTariff(ctx, ev.AccountID, nil); err != nil {
				return err
			}
		case ev.HasSnapshot:
			if err := tx.UpsertAccountTariff(ctx, ev.AccountID, &out.TariffSnapshotRecord{
				WeekBitmap: ev.WeekBitmap, Timezone: ev.Timezone, ValidUntil: ev.ValidUntil, ActivatedAt: now,
			}); err != nil {
				return err
			}
		}

		rows, err := tx.ListProgress(ctx, ev.AccountID)
		if err != nil {
			return err
		}
		for _, row := range rows {
			switch {
			case clear:
				row.Tariff = nil
			case ev.HasSnapshot:
				row.Tariff = &out.TariffSnapshotRecord{
					WeekBitmap: ev.WeekBitmap, Timezone: ev.Timezone, ValidUntil: ev.ValidUntil, ActivatedAt: now,
				}
			}
			row.LastUpdatedAt = now
			if err := tx.UpdateProgress(ctx, row); err != nil {
				return err
			}
			if row.Status == string(valobj.ProgramStatusActive) {
				if err := tx.InsertSnapshot(ctx, out.SnapshotRecord{
					AccountID: ev.AccountID, ProgramID: row.ProgramID,
					CompletionPercent: row.CompletionPercent,
					Reason:            string(valobj.SnapshotTariffChanged), CreatedAt: now,
				}); err != nil {
					return err
				}
			}
		}

		var grantedCount int
		if grant || revoke {
			if _, err := tx.RevokeAccessibleLessons(ctx, ev.AccountID, now); err != nil {
				return err
			}
		}
		if grant && progress != nil && len(prog.Lessons) > 0 {
			active := pickCurrentProgress(rows)
			if active != nil && active.CompletedLessons < active.TotalLessons {
				if next, ok := lessonForCompleted(prog, active.CompletedLessons); ok {
					if err := tx.InsertAccessibleLesson(ctx, out.AccessibleLessonRecord{
						AccountID: ev.AccountID, LessonID: next.LessonID, ProgramID: active.ProgramID,
						LessonType: next.LessonType, OrderPosition: next.OrderPosition, GrantedAt: now,
					}); err != nil {
						return err
					}
					grantedCount = 1
				}
			}
		}

		var until any
		if ev.HasSnapshot && ev.ValidUntil != nil {
			until = ev.ValidUntil.UTC().Format(time.RFC3339)
		}
		evt, err := newOutbox(ctx, "ProgramAccessChanged", map[string]any{
			"accountId": ev.AccountID.String(), "reason": ev.EventType,
			"accessibleUntil": until, "accessibleCount": grantedCount,
		}, now)
		if err != nil {
			return err
		}
		return tx.InsertOutbox(ctx, evt)
	})
	if err != nil {
		return err
	}
	s.invalidate(ctx, ev.AccountID)
	s.log.Info("program_access_changed",
		zap.String("account_id", ev.AccountID.String()), zap.String("reason", ev.EventType), zap.Bool("granted", grant))
	return nil
}

// OnMigrationRequested migrates one batch of patients to a target program version.
func (s *EventService) OnMigrationRequested(ctx context.Context, ev in.MigrationEvent) error {
	prog, err := s.catalog.GetProgram(ctx, ev.ProgramID.String(), ev.TargetVersion)
	if err != nil {
		return err
	}

	for _, accountID := range ev.AccountIDs {
		if _, err := s.repo.GetProgressByProgram(ctx, accountID, ev.ProgramID); err != nil {
			if errors.Is(err, corerr.ErrNoActiveProgram) {
				continue // patient is not on this program: skip per spec
			}
			return err
		}

		err = s.uow.Do(ctx, func(ctx context.Context, tx out.Tx) error {
			now := s.now()
			key := ev.EventID + ":" + accountID.String()
			fresh, err := tx.RecordIdempotency(ctx, out.IdempotencyRecord{EventID: key, ExpiresAt: now.Add(idempotencyTTL)})
			if err != nil {
				return err
			}
			if !fresh {
				return nil
			}

			row, err := tx.GetProgressByProgramForUpdate(ctx, accountID, ev.ProgramID)
			if err != nil {
				return err
			}
			row.ProgramVersion = prog.Version
			row.TotalLessons = prog.TotalLessons
			row.CompletionPercent = int(valobj.CalculatePercent(row.CompletedLessons, prog.TotalLessons))
			row.LastUpdatedAt = now
			if err := tx.UpdateProgress(ctx, *row); err != nil {
				return err
			}
			if _, err := tx.RevokeAccessibleLessons(ctx, accountID, now); err != nil {
				return err
			}
			if row.CompletedLessons < row.TotalLessons {
				if next, ok := lessonForCompleted(prog, row.CompletedLessons); ok {
					if err := tx.InsertAccessibleLesson(ctx, out.AccessibleLessonRecord{
						AccountID: accountID, LessonID: next.LessonID, ProgramID: ev.ProgramID,
						LessonType: next.LessonType, OrderPosition: next.OrderPosition, GrantedAt: now,
					}); err != nil {
						return err
					}
				}
			}
			outEv, err := newOutbox(ctx, "MigrationCompleted", map[string]any{
				"migration_job_id": ev.JobID, "accountId": accountID.String(),
				"programId": ev.ProgramID.String(), "targetVersion": prog.Version,
				"completedAt": now.Format(time.RFC3339), "status": "success",
			}, now)
			if err != nil {
				return err
			}
			return tx.InsertOutbox(ctx, outEv)
		})
		if err != nil {
			return err
		}
		s.invalidate(ctx, accountID)
	}
	return nil
}

// OnLessonAdded updates total lessons and recalculates completion for active and paused patients.
func (s *EventService) OnLessonAdded(ctx context.Context, ev in.LessonAddedEvent) error {
	rows, err := s.repo.ListProgressByProgram(ctx, ev.ProgramID)
	if err != nil {
		return err
	}
	err = s.uow.Do(ctx, func(ctx context.Context, tx out.Tx) error {
		now := s.now()
		fresh, err := tx.RecordIdempotency(ctx, out.IdempotencyRecord{EventID: ev.EventID, ExpiresAt: now.Add(idempotencyTTL)})
		if err != nil {
			return err
		}
		if !fresh {
			return nil
		}
		locked, err := tx.ListProgressByProgramForUpdate(ctx, ev.ProgramID)
		if err != nil {
			return err
		}
		for _, row := range locked {
			// completed programs keep a frozen percentage.
			if row.Status == string(valobj.ProgramStatusCompleted) {
				continue
			}
			row.TotalLessons = ev.NewTotalLessons
			row.CompletionPercent = int(valobj.CalculatePercent(row.CompletedLessons, ev.NewTotalLessons))
			row.LastUpdatedAt = now
			if err := tx.UpdateProgress(ctx, row); err != nil {
				return err
			}
			if row.Status == string(valobj.ProgramStatusActive) && ev.Position == row.CompletedLessons+1 {
				if _, err := tx.RevokeAccessibleLessons(ctx, row.AccountID, now); err != nil {
					return err
				}
				if err := tx.InsertAccessibleLesson(ctx, out.AccessibleLessonRecord{
					AccountID: row.AccountID, LessonID: ev.LessonID, ProgramID: ev.ProgramID,
					LessonType: ev.LessonType, OrderPosition: ev.Position, GrantedAt: now,
				}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, r := range rows {
		s.invalidate(ctx, r.AccountID)
	}
	s.log.Info("program_lessons_total_updated",
		zap.String("program_id", ev.ProgramID.String()), zap.Int("new_total", ev.NewTotalLessons), zap.Int("patients", len(rows)))
	return nil
}

func (s *EventService) invalidate(ctx context.Context, accountID uuid.UUID) {
	s.cache.del(ctx, progressKey(accountID), accessibleKey(accountID), activeProgramKey(accountID))
}

// tariffEffect maps a BC#2 event to access operations.
func tariffEffect(eventType string) (grant, revoke, clear, keepSnapshot bool) {
	switch eventType {
	case "TariffPlanActivated", "SubscriptionActivated":
		return true, true, false, false
	case "TariffPlanChanged", "SubscriptionChanged":
		return true, true, false, false
	case "TariffPlanDeActivated":
		return false, true, true, false
	case "SubscriptionSuspended", "SubscriptionExpired":
		return false, true, false, true
	case "SubscriptionResumed":
		return true, true, false, true
	case "SubscriptionRenewed":
		return true, true, false, false
	default:
		return false, false, false, false
	}
}

var (
	_ in.EventSink = (*EventService)(nil)
)
