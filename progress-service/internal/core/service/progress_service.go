package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	corerr "github.com/Reddetk/SayMoDev/progress-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/progress-service/internal/core/entity"
	"github.com/Reddetk/SayMoDev/progress-service/internal/core/valobj"
	"github.com/Reddetk/SayMoDev/progress-service/internal/logger"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

// ProgressService implements the patient-facing program and progress use cases.
type ProgressService struct {
	repo    out.Repository
	uow     out.UnitOfWork
	catalog out.ContentCatalog
	cache   cacheRec
	log     logger.Logger
	now     func() time.Time

	defaultBitmap int
}

// NewProgressService wires a patient-facing service.
func NewProgressService(repo out.Repository, uow out.UnitOfWork, catalog out.ContentCatalog, cache out.Cache, log logger.Logger, cacheTTL time.Duration, defaultBitmap int) *ProgressService {
	return &ProgressService{
		repo:          repo,
		uow:           uow,
		catalog:       catalog,
		cache:         cacheRec{cache: cache, log: log, localTTL: cacheTTL},
		log:           log,
		now:           func() time.Time { return time.Now().UTC() },
		defaultBitmap: defaultBitmap,
	}
}

// GetMyProgress implements GET /progress/me with L1→L2→L3 read path.
func (s *ProgressService) GetMyProgress(ctx context.Context, accountID uuid.UUID) (in.ProgressView, error) {
	ctx, span := tracer.Start(ctx, "progress.read.total")
	defer span.End()
	now := s.now()

	rec, err := s.loadProgress(ctx, accountID)
	if err != nil {
		return in.ProgressView{}, err
	}

	program, err := s.loadProgram(ctx, accountID, rec.ProgramID)
	if err != nil {
		return in.ProgressView{}, err
	}

	accessible, err := s.loadAccessible(ctx, accountID)
	if err != nil {
		return in.ProgressView{}, err
	}
	if accessible != nil && accessible.ProgramID != rec.ProgramID {
		accessible = nil
	}

	agg := toProgressEntity(*rec)
	available, next, reason := agg.Unlock(now)
	var accView *in.AccessibleLessonView
	if accessible != nil {
		accView = accessibleView(accessible, available, next, reason)
	}

	return in.ProgressView{
		AccountID:         rec.AccountID.String(),
		ProgramID:         rec.ProgramID.String(),
		Status:            rec.Status,
		CompletedLessons:  rec.CompletedLessons,
		TotalLessons:      rec.TotalLessons,
		CompletionPercent: rec.CompletionPercent,
		ActiveProgram:     activeProgramView(program),
		TariffSnapshot:    tariffView(agg.Tariff),
		AccessibleLesson:  accView,
		LastUpdatedAt:     rec.LastUpdatedAt.UTC(),
	}, nil
}

// Activate implements POST /progress/me/activate (Kafka fallback path).
func (s *ProgressService) Activate(ctx context.Context, cmd in.ActivateCommand) (in.ActivateResult, error) {
	ctx, span := tracer.Start(ctx, "progress.program_activate.total")
	defer span.End()
	prog, err := s.resolveProgram(ctx, cmd)
	if err != nil {
		return in.ActivateResult{}, err
	}

	var result in.ActivateResult
	err = s.uow.Do(ctx, func(ctx context.Context, tx out.Tx) error {
		now := s.now()
		first, err := activateInTx(ctx, tx, cmd.AccountID, prog, cmd.Difficulty, cmd.AphasiaType, s.defaultBitmap, now)
		if err != nil {
			return err
		}
		result = in.ActivateResult{
			AccountID:         cmd.AccountID.String(),
			ProgramID:         prog.ProgramID.String(),
			Status:            string(valobj.ProgramStatusActive),
			CompletionPercent: 0,
			TotalLessons:      prog.TotalLessons,
			AccessibleLesson:  grantedView(first),
		}
		return nil
	})
	if err != nil {
		return in.ActivateResult{}, err
	}

	s.cache.del(ctx, progressKey(cmd.AccountID), accessibleKey(cmd.AccountID), activeProgramKey(cmd.AccountID))
	s.log.Info("program_activated", zap.String("account_id", cmd.AccountID.String()), zap.String("program_id", prog.ProgramID.String()))
	return result, nil
}

// ChangeDifficulty implements PATCH /progress/difficulty (patient and admin share the core).
func (s *ProgressService) ChangeDifficulty(ctx context.Context, cmd in.ChangeDifficultyCommand) (in.ChangeDifficultyResult, error) {
	ctx, span := tracer.Start(ctx, "progress.difficulty.total")
	defer span.End()
	newDifficulty, err := valobj.ParseDifficulty(cmd.NewDifficulty)
	if err != nil {
		return in.ChangeDifficultyResult{}, corerr.ErrInvalidDifficulty
	}

	active, err := s.repo.GetActiveProgram(ctx, cmd.AccountID)
	if err != nil {
		if errors.Is(err, corerr.ErrNoActiveProgram) {
			return in.ChangeDifficultyResult{}, corerr.ErrNoActiveProgram
		}
		return in.ChangeDifficultyResult{}, err
	}
	if active.Difficulty == string(newDifficulty) && (cmd.AphasiaType == "" || cmd.AphasiaType == active.AphasiaType) {
		return in.ChangeDifficultyResult{}, corerr.ErrSameDifficulty
	}

	aphasia := cmd.AphasiaType
	if aphasia == "" {
		aphasia = active.AphasiaType
	}
	prog, err := s.catalog.ResolveProgram(ctx, string(newDifficulty), aphasia)
	if err != nil {
		return in.ChangeDifficultyResult{}, err
	}
	first, ok := firstLesson(prog)
	if !ok {
		return in.ChangeDifficultyResult{}, corerr.ErrProgramCatalogUnavailable
	}

	var result in.ChangeDifficultyResult
	err = s.uow.Do(ctx, func(ctx context.Context, tx out.Tx) error {
		now := s.now()

		cur, err := tx.GetActiveProgramForUpdate(ctx, cmd.AccountID)
		if err != nil {
			return err
		}
		if cur.ProgramID == prog.ProgramID {
			return corerr.ErrSameDifficulty
		}

		oldProgress, err := tx.GetProgressByProgramForUpdate(ctx, cmd.AccountID, cur.ProgramID)
		if err != nil {
			return err
		}

		// Freeze the old program and keep progress history.
		cur.Status = string(valobj.ProgramStatusPaused)
		cur.PausedAt = utcPtr(&now)
		cur.CompletionPercent = oldProgress.CompletionPercent
		if err := tx.UpdateActiveProgram(ctx, *cur); err != nil {
			return err
		}
		oldProgress.Status = string(valobj.ProgramStatusPaused)
		oldProgress.LastUpdatedAt = now
		if err := tx.UpdateProgress(ctx, *oldProgress); err != nil {
			return err
		}
		if err := tx.InsertSnapshot(ctx, out.SnapshotRecord{
			AccountID:         cmd.AccountID,
			ProgramID:         cur.ProgramID,
			CompletionPercent: oldProgress.CompletionPercent,
			Reason:            string(valobj.SnapshotDifficultyChanged),
			CreatedAt:         now,
		}); err != nil {
			return err
		}
		if _, err := tx.RevokeAccessibleLessons(ctx, cmd.AccountID, now); err != nil {
			return err
		}

		newAP := out.ActiveProgramRecord{
			AccountID:   cmd.AccountID,
			ProgramID:   prog.ProgramID,
			Version:     prog.Version,
			Status:      string(valobj.ProgramStatusActive),
			Difficulty:  string(newDifficulty),
			AphasiaType: aphasia,
			ActivatedAt: now,
		}
		if err := tx.InsertActiveProgram(ctx, newAP); err != nil {
			return err
		}

		tariff := oldProgress.Tariff
		if tariff == nil {
			if t := defaultTariff(now, s.defaultBitmap); t != nil {
				tariff = &out.TariffSnapshotRecord{WeekBitmap: t.WeekBitmap, Timezone: t.Timezone, ActivatedAt: t.ActivatedAt}
			}
		}
		np := out.ProgressRecord{
			AccountID:         cmd.AccountID,
			ProgramID:         prog.ProgramID,
			ProgramVersion:    prog.Version,
			Status:            string(valobj.ProgramStatusActive),
			TotalLessons:      prog.TotalLessons,
			CompletionPercent: 0,
			Tariff:            tariff,
			LastUpdatedAt:     now,
		}
		if err := tx.InsertProgress(ctx, np); err != nil {
			return err
		}
		if err := tx.InsertAccessibleLesson(ctx, out.AccessibleLessonRecord{
			AccountID: cmd.AccountID, LessonID: first.LessonID, ProgramID: prog.ProgramID,
			LessonType: first.LessonType, OrderPosition: first.OrderPosition, GrantedAt: now,
		}); err != nil {
			return err
		}

		events := []struct {
			typ     string
			payload map[string]any
		}{
			{"ProgramDifficultyChanged", map[string]any{
				"accountId":     cmd.AccountID.String(),
				"oldProgramId":  cur.ProgramID.String(),
				"newProgramId":  prog.ProgramID.String(),
				"newClassifier": map[string]string{"difficulty": string(newDifficulty), "aphasiaType": aphasia},
			}},
			{"ProgramCompleted", map[string]any{
				"accountId": cmd.AccountID.String(), "programId": cur.ProgramID.String(),
				"completionPercent": oldProgress.CompletionPercent, "completedAt": now.Format(time.RFC3339),
			}},
			{"ProgramActivated", map[string]any{
				"accountId": cmd.AccountID.String(), "programId": prog.ProgramID.String(),
				"version": prog.Version, "activatedAt": now.Format(time.RFC3339),
			}},
		}
		for _, e := range events {
			ev, err := newOutbox(ctx, e.typ, e.payload, now)
			if err != nil {
				return err
			}
			if err := tx.InsertOutbox(ctx, ev); err != nil {
				return err
			}
		}

		result = in.ChangeDifficultyResult{
			OldProgramID:         cur.ProgramID.String(),
			NewProgramID:         prog.ProgramID.String(),
			OldCompletionPercent: oldProgress.CompletionPercent,
			NewCompletionPercent: 0,
			Message:              "difficulty changed; progress frozen on old program",
		}
		return nil
	})
	if err != nil {
		return in.ChangeDifficultyResult{}, err
	}

	s.cache.del(ctx, progressKey(cmd.AccountID), accessibleKey(cmd.AccountID), activeProgramKey(cmd.AccountID))
	s.log.Info("program_difficulty_changed",
		zap.String("account_id", cmd.AccountID.String()),
		zap.String("old_program_id", result.OldProgramID),
		zap.String("new_program_id", result.NewProgramID))
	return result, nil
}

// Resume implements POST /progress/programs/{programId}/resume.
func (s *ProgressService) Resume(ctx context.Context, cmd in.ResumeCommand) (in.ResumeResult, error) {
	ctx, span := tracer.Start(ctx, "progress.resume.total")
	defer span.End()
	programs, err := s.repo.ListPrograms(ctx, cmd.AccountID)
	if err != nil {
		return in.ResumeResult{}, err
	}
	var target *out.ActiveProgramRecord
	activeCompleted := true
	for i := range programs {
		if programs[i].ProgramID == cmd.ProgramID && programs[i].Status == string(valobj.ProgramStatusPaused) {
			target = &programs[i]
		}
		if programs[i].Status == string(valobj.ProgramStatusActive) {
			return in.ResumeResult{}, corerr.ErrCurrentProgramNotCompleted
		}
	}
	if target == nil {
		// Distinguish "exists in catalog but not paused" from "program no longer exists" (both 404).
		if _, err := s.catalog.GetProgram(ctx, cmd.ProgramID.String(), 0); err != nil {
			if errors.Is(err, corerr.ErrProgramNotFound) {
				return in.ResumeResult{}, corerr.ErrProgramNoLongerExists
			}
			return in.ResumeResult{}, err
		}
		return in.ResumeResult{}, corerr.ErrProgramNotPaused
	}
	if !activeCompleted {
		return in.ResumeResult{}, corerr.ErrCurrentProgramNotCompleted
	}

	prog, err := s.catalog.GetProgram(ctx, cmd.ProgramID.String(), target.Version)
	if err != nil {
		if errors.Is(err, corerr.ErrProgramNotFound) {
			return in.ResumeResult{}, corerr.ErrProgramNoLongerExists
		}
		return in.ResumeResult{}, err
	}

	var result in.ResumeResult
	err = s.uow.Do(ctx, func(ctx context.Context, tx out.Tx) error {
		now := s.now()
		cur, err := tx.GetActiveProgramForUpdate(ctx, cmd.AccountID)
		if err != nil && !errors.Is(err, corerr.ErrNoActiveProgram) {
			return err
		}
		if cur != nil {
			return corerr.ErrCurrentProgramNotCompleted
		}

		// Lock all programs (target + completed one) and flip the target to active.
		all, err := tx.ListPrograms(ctx, cmd.AccountID)
		if err != nil {
			return err
		}
		var resumed *out.ActiveProgramRecord
		for i := range all {
			if all[i].ProgramID == cmd.ProgramID && all[i].Status == string(valobj.ProgramStatusPaused) {
				resumed = &all[i]
			}
		}
		if resumed == nil {
			return corerr.ErrProgramNotPaused
		}
		resumed.Status = string(valobj.ProgramStatusActive)
		resumed.PausedAt = nil
		resumed.ActivatedAt = now
		if err := tx.UpdateActiveProgram(ctx, *resumed); err != nil {
			return err
		}

		pr, err := tx.GetProgressByProgramForUpdate(ctx, cmd.AccountID, cmd.ProgramID)
		if err != nil {
			return err
		}
		pr.Status = string(valobj.ProgramStatusActive)
		pr.ProgramVersion = prog.Version
		pr.TotalLessons = prog.TotalLessons
		pr.CompletionPercent = int(valobj.CalculatePercent(pr.CompletedLessons, prog.TotalLessons))
		pr.LastUpdatedAt = now
		if err := tx.UpdateProgress(ctx, *pr); err != nil {
			return err
		}
		if _, err := tx.RevokeAccessibleLessons(ctx, cmd.AccountID, now); err != nil {
			return err
		}

		var granted *out.LessonInfo
		if next, ok := lessonForCompleted(prog, pr.CompletedLessons); ok {
			granted = &next
			if err := tx.InsertAccessibleLesson(ctx, out.AccessibleLessonRecord{
				AccountID: cmd.AccountID, LessonID: next.LessonID, ProgramID: cmd.ProgramID,
				LessonType: next.LessonType, OrderPosition: next.OrderPosition, GrantedAt: now,
			}); err != nil {
				return err
			}
		}

		ev, err := newOutbox(ctx, "ProgramActivated", map[string]any{
			"accountId": cmd.AccountID.String(), "programId": cmd.ProgramID.String(),
			"version": prog.Version, "activatedAt": now.Format(time.RFC3339),
		}, now)
		if err != nil {
			return err
		}
		if err := tx.InsertOutbox(ctx, ev); err != nil {
			return err
		}

		result = in.ResumeResult{
			ProgramID:         cmd.ProgramID.String(),
			Status:            string(valobj.ProgramStatusActive),
			CompletionPercent: pr.CompletionPercent,
			AccessibleLesson:  nil,
			Message:           "program resumed; progress restored",
		}
		if granted != nil {
			result.AccessibleLesson = grantedView(*granted)
		}
		return nil
	})
	if err != nil {
		return in.ResumeResult{}, err
	}

	s.cache.del(ctx, progressKey(cmd.AccountID), accessibleKey(cmd.AccountID), activeProgramKey(cmd.AccountID))
	return result, nil
}

// DeleteMyProgress implements DELETE /progress/me (GDPR).
func (s *ProgressService) DeleteMyProgress(ctx context.Context, cmd in.DeleteCommand) error {
	ctx, span := tracer.Start(ctx, "progress.delete.total")
	defer span.End()
	const confirmation = "delete-my-progress"
	if cmd.Confirm != confirmation {
		return corerr.ErrConfirmationRequired
	}

	err := s.uow.Do(ctx, func(ctx context.Context, tx out.Tx) error {
		now := s.now()
		return deleteAccountData(ctx, tx, cmd.AccountID, now)
	})
	if err != nil {
		return err
	}

	s.cache.del(ctx, progressKey(cmd.AccountID), accessibleKey(cmd.AccountID), activeProgramKey(cmd.AccountID))
	s.log.Info("progress_deleted", zap.String("account_id", cmd.AccountID.String()))
	return nil
}

func (s *ProgressService) resolveProgram(ctx context.Context, cmd in.ActivateCommand) (out.ProgramInfo, error) {
	if cmd.ProgramID != nil {
		return s.catalog.GetProgram(ctx, cmd.ProgramID.String(), 0)
	}
	if !cmd.HasClassifier || cmd.Difficulty == "" {
		return out.ProgramInfo{}, corerr.ErrNoProgramForClassifier
	}
	return s.catalog.ResolveProgram(ctx, cmd.Difficulty, cmd.AphasiaType)
}

func (s *ProgressService) loadProgress(ctx context.Context, accountID uuid.UUID) (*out.ProgressRecord, error) {
	var rec out.ProgressRecord
	if s.cache.get(ctx, progressKey(accountID), &rec) {
		return &rec, nil
	}
	got, err := s.repo.GetCurrentProgress(ctx, accountID)
	if err != nil {
		return nil, err
	}
	s.cache.set(ctx, progressKey(accountID), got)
	return got, nil
}

func (s *ProgressService) loadAccessible(ctx context.Context, accountID uuid.UUID) (*out.AccessibleLessonRecord, error) {
	var rec out.AccessibleLessonRecord
	if s.cache.get(ctx, accessibleKey(accountID), &rec) {
		return &rec, nil
	}
	got, err := s.repo.GetAccessibleLesson(ctx, accountID)
	if err != nil {
		if errors.Is(err, corerr.ErrNoActiveProgram) {
			return nil, nil
		}
		return nil, err
	}
	s.cache.set(ctx, accessibleKey(accountID), got)
	return got, nil
}

func (s *ProgressService) loadProgram(ctx context.Context, accountID, programID uuid.UUID) (out.ActiveProgramRecord, error) {
	var rec out.ActiveProgramRecord
	if s.cache.get(ctx, activeProgramKey(accountID), &rec) && rec.ProgramID == programID {
		return rec, nil
	}
	programs, err := s.repo.ListPrograms(ctx, accountID)
	if err != nil {
		return out.ActiveProgramRecord{}, err
	}
	var picked *out.ActiveProgramRecord
	for i := range programs {
		if programs[i].ProgramID == programID {
			picked = &programs[i]
			if programs[i].Status == string(valobj.ProgramStatusActive) {
				break
			}
		}
	}
	if picked == nil {
		return out.ActiveProgramRecord{}, corerr.ErrNoActiveProgram
	}
	s.cache.set(ctx, activeProgramKey(accountID), picked)
	return *picked, nil
}

// compile-time checks
var (
	_ in.ProgressReader  = (*ProgressService)(nil)
	_ in.ProgramOperator = (*ProgressService)(nil)
	_ in.SelfDeleter     = (*ProgressService)(nil)
	_                    = entity.PatientProgress{}
)
