package service

import (
	"context"
	"errors"
	"net/url"
	"strings"
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

const answersBucketPrefix = "https://storage.yandexcloud.net/saymo-answers/"

// LessonService implements answer submission, presign and explicit completion (Model B).
type LessonService struct {
	repo          out.Repository
	uow           out.UnitOfWork
	catalog       out.ContentCatalog
	storage       out.ObjectStorage
	cache         cacheRec
	log           logger.Logger
	now           func() time.Time
	presignPutTTL time.Duration
}

// NewLessonService wires the lesson use cases.
func NewLessonService(repo out.Repository, uow out.UnitOfWork, catalog out.ContentCatalog, storage out.ObjectStorage, cache out.Cache, log logger.Logger, cacheTTL, presignPutTTL time.Duration) *LessonService {
	return &LessonService{
		repo:          repo,
		uow:           uow,
		catalog:       catalog,
		storage:       storage,
		cache:         cacheRec{cache: cache, log: log, localTTL: cacheTTL},
		log:           log,
		now:           func() time.Time { return time.Now().UTC() },
		presignPutTTL: presignPutTTL,
	}
}

// Presign implements POST /progress/lessons/{lessonId}/answers/presign.
func (s *LessonService) Presign(ctx context.Context, cmd in.PresignCommand) (in.PresignResult, error) {
	ctx, span := tracer.Start(ctx, "progress.answer_presign.total")
	defer span.End()
	media := valobj.MediaType(cmd.MediaType)
	if media != valobj.MediaTypePhoto && media != valobj.MediaTypeVideo {
		return in.PresignResult{}, corerr.ErrInvalidMediaType
	}
	ext, ok := valobj.ExtForContentType(cmd.ContentType)
	if !ok {
		return in.PresignResult{}, corerr.ErrInvalidMediaType
	}
	if !s.storage.Configured() {
		return in.PresignResult{}, corerr.ErrS3NotConfigured
	}

	if _, err := s.requireAccessible(ctx, cmd.AccountID, cmd.LessonID); err != nil {
		return in.PresignResult{}, err
	}

	now := s.now()
	// goca:manual — object key layout is fixed by classifications/ProgressTrackingAndAssessment.md.
	key := "answers/" + cmd.AccountID.String() + "/" + cmd.LessonID.String() + "/" + uuid.NewString() + ext
	presigned, err := s.storage.PresignPut(ctx, key, cmd.ContentType, s.presignPutTTL)
	if err != nil {
		return in.PresignResult{}, err
	}
	return in.PresignResult{
		PresignedPutURL: presigned,
		ObjectKey:       key,
		ExpiresAt:       now.Add(s.presignPutTTL),
	}, nil
}

// Submit implements POST /progress/lessons/{lessonId}/answers (repeatable, append-only).
func (s *LessonService) Submit(ctx context.Context, cmd in.SubmitAnswerCommand) (in.SubmitAnswerResult, error) {
	ctx, span := tracer.Start(ctx, "progress.lesson_answer.total")
	defer span.End()
	media := valobj.MediaType(cmd.MediaType)
	if !media.Valid() {
		return in.SubmitAnswerResult{}, corerr.ErrInvalidMediaType
	}
	if err := validatePayload(media, cmd.Payload); err != nil {
		return in.SubmitAnswerResult{}, err
	}
	accessible, err := s.requireAccessible(ctx, cmd.AccountID, cmd.LessonID)
	if err != nil {
		return in.SubmitAnswerResult{}, err
	}

	now := s.now()
	answer := out.AnswerRecord{
		AnswerID:    uuid.New(),
		AccountID:   cmd.AccountID,
		LessonID:    cmd.LessonID,
		ProgramID:   accessible.ProgramID,
		MediaType:   string(media),
		Payload:     cmd.Payload,
		SubmittedAt: now,
	}

	err = s.uow.Do(ctx, func(ctx context.Context, tx out.Tx) error {
		// Lock the progress row: completion and answer submission must serialise on the
		// same aggregate (a concurrent complete must win before a new answer is accepted).
		if _, err := tx.GetProgressForUpdate(ctx, cmd.AccountID); err != nil {
			return err
		}
		done, err := tx.HasCompletion(ctx, cmd.AccountID, cmd.LessonID)
		if err != nil {
			return err
		}
		if done {
			return corerr.ErrLessonAlreadyCompleted
		}
		if err := tx.InsertAnswer(ctx, answer); err != nil {
			return err
		}
		ev, err := newOutbox(ctx, "LessonAnswersSubmitted", map[string]any{
			"accountId":   cmd.AccountID.String(),
			"lessonId":    cmd.LessonID.String(),
			"answerId":    answer.AnswerID.String(),
			"mediaType":   answer.MediaType,
			"submittedAt": now.Format(time.RFC3339),
		}, now)
		if err != nil {
			return err
		}
		return tx.InsertOutbox(ctx, ev)
	})
	if err != nil {
		return in.SubmitAnswerResult{}, err
	}

	s.log.Info("lesson_answer_submitted",
		zap.String("account_id", cmd.AccountID.String()),
		zap.String("lesson_id", cmd.LessonID.String()),
		zap.String("media_type", string(media)))
	return in.SubmitAnswerResult{
		AnswerID:    answer.AnswerID.String(),
		SubmittedAt: now,
		Message:     "answer saved; call /complete to finish lesson",
	}, nil
}

// Complete implements POST /progress/lessons/{lessonId}/complete (irreversible).
func (s *LessonService) Complete(ctx context.Context, cmd in.CompleteCommand) (in.CompleteResult, error) {
	ctx, span := tracer.Start(ctx, "progress.lesson_complete.total")
	defer span.End()
	accessible, err := s.requireAccessible(ctx, cmd.AccountID, cmd.LessonID)
	if err != nil {
		return in.CompleteResult{}, err
	}

	progressRec, err := s.repo.GetCurrentProgress(ctx, cmd.AccountID)
	if err != nil {
		return in.CompleteResult{}, err
	}
	if progressRec.ProgramID != accessible.ProgramID {
		return in.CompleteResult{}, corerr.ErrLessonNotAccessible
	}

	// Guard: regular lessons need at least one answer; creative lessons skip it.
	if accessible.LessonType == string(valobj.LessonTypeRegular) {
		count, err := s.repo.CountAnswers(ctx, cmd.AccountID, cmd.LessonID)
		if err != nil {
			return in.CompleteResult{}, err
		}
		if count == 0 {
			return in.CompleteResult{}, corerr.ErrNoAnswerSubmitted
		}
	}

	// Sync BC#3 read stays outside the transaction (spec: external calls before BEGIN).
	prog, err := s.catalog.GetProgram(ctx, accessible.ProgramID.String(), 0)
	if err != nil {
		if errors.Is(err, corerr.ErrProgramNotFound) {
			return in.CompleteResult{}, corerr.ErrProgramCatalogUnavailable
		}
		return in.CompleteResult{}, err
	}

	var result in.CompleteResult
	err = s.uow.Do(ctx, func(ctx context.Context, tx out.Tx) error {
		now := s.now()

		done, err := tx.HasCompletion(ctx, cmd.AccountID, cmd.LessonID)
		if err != nil {
			return err
		}
		if done {
			return corerr.ErrLessonAlreadyCompleted
		}

		progressRow, err := tx.GetProgressForUpdate(ctx, cmd.AccountID)
		if err != nil {
			return err
		}
		if progressRow.ProgramID != accessible.ProgramID {
			return corerr.ErrLessonNotAccessible
		}

		granted, err := tx.GetAccessibleLessonForUpdate(ctx, cmd.AccountID)
		if err != nil {
			if errors.Is(err, corerr.ErrNoActiveProgram) {
				return corerr.ErrLessonNotAccessible
			}
			return err
		}
		if granted.LessonID != cmd.LessonID {
			return corerr.ErrLessonNotAccessible
		}
		agg := toProgressEntity(*progressRow)
		if available, _, _ := agg.Unlock(now); !available {
			return corerr.ErrLessonNotAccessible
		}
		if progressRow.TotalLessons != prog.TotalLessons && prog.TotalLessons > 0 {
			progressRow.TotalLessons = prog.TotalLessons
		}

		if err := tx.InsertCompletion(ctx, out.CompletionRecord{
			AccountID: cmd.AccountID, LessonID: cmd.LessonID,
			ProgramID: accessible.ProgramID, CompletedAt: now,
		}); err != nil {
			return err
		}

		agg.CompleteLesson(now)
		updated := toProgressRecord(agg)
		if agg.Finished() {
			updated.Status = string(valobj.ProgramStatusCompleted)
		}
		if err := tx.UpdateProgress(ctx, updated); err != nil {
			return err
		}
		if ap, err := tx.GetActiveProgramForUpdate(ctx, cmd.AccountID); err == nil && ap != nil {
			ap.CompletionPercent = int(agg.CompletionPercent)
			if err := tx.UpdateActiveProgram(ctx, *ap); err != nil {
				return err
			}
		} else if err != nil && !errors.Is(err, corerr.ErrNoActiveProgram) {
			return err
		}
		if _, err := tx.RevokeAccessibleLessons(ctx, cmd.AccountID, now); err != nil {
			return err
		}

		var next *out.LessonInfo
		if !agg.Finished() {
			if candidate, ok := lessonInfoAt(prog, granted.OrderPosition+1); ok {
				next = &candidate
				if err := tx.InsertAccessibleLesson(ctx, out.AccessibleLessonRecord{
					AccountID: cmd.AccountID, LessonID: candidate.LessonID, ProgramID: accessible.ProgramID,
					LessonType: candidate.LessonType, OrderPosition: candidate.OrderPosition, GrantedAt: now,
				}); err != nil {
					return err
				}
			}
		}

		lessonEv, err := newOutbox(ctx, "LessonCompleted", map[string]any{
			"accountId":         cmd.AccountID.String(),
			"lessonId":          cmd.LessonID.String(),
			"completionPercent": int(agg.CompletionPercent),
			"completedAt":       now.Format(time.RFC3339),
		}, now)
		if err != nil {
			return err
		}
		if err := tx.InsertOutbox(ctx, lessonEv); err != nil {
			return err
		}

		if agg.Finished() {
			ap, err := tx.GetActiveProgramForUpdate(ctx, cmd.AccountID)
			if err != nil && !errors.Is(err, corerr.ErrNoActiveProgram) {
				return err
			}
			if ap != nil {
				ap.Status = string(valobj.ProgramStatusCompleted)
				ap.CompletedAt = utcPtr(&now)
				ap.CompletionPercent = int(agg.CompletionPercent)
				if err := tx.UpdateActiveProgram(ctx, *ap); err != nil {
					return err
				}
			}
			if err := tx.InsertSnapshot(ctx, out.SnapshotRecord{
				AccountID: cmd.AccountID, ProgramID: accessible.ProgramID,
				CompletionPercent: int(agg.CompletionPercent),
				Reason:            string(valobj.SnapshotProgramCompleted), CreatedAt: now,
			}); err != nil {
				return err
			}
			progEv, err := newOutbox(ctx, "ProgramCompleted", map[string]any{
				"accountId":         cmd.AccountID.String(),
				"programId":         accessible.ProgramID.String(),
				"completionPercent": int(agg.CompletionPercent),
				"completedAt":       now.Format(time.RFC3339),
			}, now)
			if err != nil {
				return err
			}
			if err := tx.InsertOutbox(ctx, progEv); err != nil {
				return err
			}
		}

		result = in.CompleteResult{CompletionPercent: int(agg.CompletionPercent)}
		if next != nil {
			id := next.LessonID.String()
			result.NextLessonID = &id
		}
		return nil
	})
	if err != nil {
		return in.CompleteResult{}, err
	}

	s.cache.del(ctx, progressKey(cmd.AccountID), accessibleKey(cmd.AccountID), activeProgramKey(cmd.AccountID))
	s.cache.setLessonCompleted(ctx, cmd.AccountID, cmd.LessonID)
	s.log.Info("lesson_completed",
		zap.String("account_id", cmd.AccountID.String()),
		zap.String("lesson_id", cmd.LessonID.String()),
		zap.Int("completion_percent", result.CompletionPercent))
	return result, nil
}

// requireAccessible verifies the lesson is granted to the patient, not completed and currently unlocked.
func (s *LessonService) requireAccessible(ctx context.Context, accountID, lessonID uuid.UUID) (*out.AccessibleLessonRecord, error) {
	// Guard order per contract: already completed wins over accessibility (409, not 403).
	done, err := s.repo.HasCompletion(ctx, accountID, lessonID)
	if err != nil {
		return nil, err
	}
	if done {
		return nil, corerr.ErrLessonAlreadyCompleted
	}
	progressRec, err := s.repo.GetCurrentProgress(ctx, accountID)
	if err != nil {
		return nil, err
	}
	accessible, err := s.repo.GetAccessibleLesson(ctx, accountID)
	if err != nil {
		if errors.Is(err, corerr.ErrNoActiveProgram) {
			return nil, corerr.ErrLessonNotAccessible
		}
		return nil, err
	}
	if accessible.LessonID != lessonID || accessible.ProgramID != progressRec.ProgramID {
		return nil, corerr.ErrLessonNotAccessible
	}
	agg := toProgressEntity(*progressRec)
	if available, _, _ := agg.Unlock(s.now()); !available {
		return nil, corerr.ErrLessonNotAccessible
	}
	return accessible, nil
}

func validatePayload(media valobj.MediaType, payload string) error {
	switch media {
	case valobj.MediaTypeText:
		if strings.TrimSpace(payload) == "" {
			return corerr.ErrInvalidPayload
		}
	case valobj.MediaTypePhoto, valobj.MediaTypeVideo:
		if !strings.HasPrefix(payload, answersBucketPrefix) {
			return corerr.ErrInvalidPayload
		}
		if _, err := url.ParseRequestURI(payload); err != nil {
			return corerr.ErrInvalidPayload
		}
	}
	return nil
}

// objectKeyFromURL extracts the S3 object key from a stored answer URL.
func objectKeyFromURL(payload string) string {
	if !strings.HasPrefix(payload, answersBucketPrefix) {
		return ""
	}
	return strings.TrimPrefix(payload, answersBucketPrefix)
}

var (
	_ in.LessonAnswerer  = (*LessonService)(nil)
	_ in.LessonCompleter = (*LessonService)(nil)
	_                    = entity.LessonAnswer{}
)
