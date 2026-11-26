package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Reddetk/SayMoDev/progress-service/internal/core/valobj"
	"github.com/Reddetk/SayMoDev/progress-service/internal/logger"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

const (
	defaultAnswersLimit = 50
	maxAnswersLimit     = 200
)

// AdminService implements /admin/* read use cases and the BC#3 deletion guard.
type AdminService struct {
	repo          out.Repository
	uow           out.UnitOfWork
	catalog       out.ContentCatalog
	storage       out.ObjectStorage
	difficulty    *ProgressService
	log           logger.Logger
	presignGetTTL time.Duration
}

// NewAdminService wires admin reads and delegates difficulty changes to the patient service core.
func NewAdminService(repo out.Repository, uow out.UnitOfWork, catalog out.ContentCatalog, storage out.ObjectStorage, difficulty *ProgressService, log logger.Logger, presignGetTTL time.Duration) *AdminService {
	return &AdminService{repo: repo, uow: uow, catalog: catalog, storage: storage, difficulty: difficulty, log: log, presignGetTTL: presignGetTTL}
}

// GetProgress implements GET /admin/progress/{accountId}.
func (s *AdminService) GetProgress(ctx context.Context, accountID uuid.UUID) (in.AdminProgressView, error) {
	rec, err := s.repo.GetCurrentProgress(ctx, accountID)
	if err != nil {
		return in.AdminProgressView{}, err
	}
	programs, err := s.repo.ListPrograms(ctx, accountID)
	if err != nil {
		return in.AdminProgressView{}, err
	}
	accessible, err := s.repo.ListAccessibleLessons(ctx, accountID)
	if err != nil {
		return in.AdminProgressView{}, err
	}

	agg := toProgressEntity(*rec)
	var current *out.ActiveProgramRecord
	paused := make([]in.PausedProgramView, 0)
	for i := range programs {
		p := programs[i]
		switch p.Status {
		case string(valobj.ProgramStatusActive):
			current = &programs[i]
		case string(valobj.ProgramStatusPaused):
			paused = append(paused, in.PausedProgramView{
				ProgramID:         p.ProgramID.String(),
				Status:            p.Status,
				ActivatedAt:       p.ActivatedAt.UTC(),
				PausedAt:          utcPtr(p.PausedAt),
				CompletionPercent: p.CompletionPercent,
			})
		}
	}
	if current == nil {
		for i := range programs {
			if programs[i].ProgramID == rec.ProgramID {
				current = &programs[i]
				break
			}
		}
	}
	var activeView *in.ActiveProgramView
	if current != nil {
		activeView = activeProgramView(*current)
	}

	accViews := make([]in.AdminAccessibleLessonView, 0, len(accessible))
	for _, a := range accessible {
		accViews = append(accViews, in.AdminAccessibleLessonView{
			LessonID:  a.LessonID.String(),
			RevokedAt: utcPtr(a.RevokedAt),
		})
	}

	return in.AdminProgressView{
		AccountID:         rec.AccountID.String(),
		Email:             "", // BC#4 does not store BC#1 PII; admin UI joins by accountId.
		ProgramID:         rec.ProgramID.String(),
		Status:            rec.Status,
		CompletedLessons:  rec.CompletedLessons,
		TotalLessons:      rec.TotalLessons,
		CompletionPercent: rec.CompletionPercent,
		TariffSnapshot:    tariffView(agg.Tariff),
		ActiveProgram:     activeView,
		AccessibleLessons: accViews,
		PausedPrograms:    paused,
		LastUpdatedAt:     rec.LastUpdatedAt.UTC(),
	}, nil
}

// ListAnswers implements GET /admin/answers/{accountId}.
func (s *AdminService) ListAnswers(ctx context.Context, q in.AdminAnswersQuery) (in.AdminAnswersPage, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultAnswersLimit
	}
	if limit > maxAnswersLimit {
		limit = maxAnswersLimit
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}

	page, err := s.repo.ListAnswers(ctx, out.AnswerFilter{
		AccountID: q.AccountID,
		LessonID:  q.LessonID,
		MediaType: q.MediaType,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		return in.AdminAnswersPage{}, err
	}
	titles := s.lessonTitles(ctx, page.Answers)
	return in.AdminAnswersPage{
		Answers: s.answerViews(ctx, page.Answers, titles),
		Total:   page.Total,
		Limit:   limit,
		Offset:  offset,
	}, nil
}

// ListLessonAnswers implements GET /admin/answers/{accountId}/{lessonId}, sorted ASC.
func (s *AdminService) ListLessonAnswers(ctx context.Context, accountID, lessonID uuid.UUID) ([]in.AdminAnswerView, error) {
	page, err := s.repo.ListAnswers(ctx, out.AnswerFilter{
		AccountID: accountID,
		LessonID:  &lessonID,
		Limit:     maxAnswersLimit,
		Offset:    0,
		Ascending: true,
	})
	if err != nil {
		return nil, err
	}
	titles := s.lessonTitles(ctx, page.Answers)
	return s.answerViews(ctx, page.Answers, titles), nil
}

// ChangeDifficulty implements PATCH /admin/progress/{accountId}/difficulty.
func (s *AdminService) ChangeDifficulty(ctx context.Context, cmd in.ChangeDifficultyCommand) (in.ChangeDifficultyResult, error) {
	res, err := s.difficulty.ChangeDifficulty(ctx, cmd)
	if err != nil {
		return in.ChangeDifficultyResult{}, err
	}
	s.log.Info("admin_difficulty_changed",
		zap.String("account_id", cmd.AccountID.String()),
		zap.String("new_program_id", res.NewProgramID))
	return res, nil
}

// ActivePatients implements GET /admin/progress/program/{programId}/version/{version}/active-patients.
func (s *AdminService) ActivePatients(ctx context.Context, programID uuid.UUID, version int) (in.ActivePatientsView, error) {
	res, err := s.repo.CountActivePatients(ctx, programID, version)
	if err != nil {
		return in.ActivePatientsView{}, err
	}
	ids := make([]string, 0, len(res.IDs))
	for _, id := range res.IDs {
		ids = append(ids, id.String())
	}
	return in.ActivePatientsView{
		ProgramID:          res.ProgramID.String(),
		Version:            res.Version,
		ActivePatientCount: res.Count,
		PatientIDs:         ids,
	}, nil
}

// lessonTitles resolves lesson titles from BC#3 for admin display; failures degrade to empty titles.
func (s *AdminService) lessonTitles(ctx context.Context, answers []out.AnswerRecord) map[string]string {
	titles := make(map[string]string)
	seen := make(map[uuid.UUID]bool)
	for _, a := range answers {
		if seen[a.ProgramID] {
			continue
		}
		seen[a.ProgramID] = true
		prog, err := s.catalog.GetProgram(ctx, a.ProgramID.String(), 0)
		if err != nil {
			s.log.Warn("bc3_lesson_titles_failed", zap.String("program_id", a.ProgramID.String()), zap.Error(err))
			continue
		}
		for _, l := range prog.Lessons {
			titles[l.LessonID.String()] = l.Title
		}
	}
	return titles
}

var (
	_ in.AdminReader   = (*AdminService)(nil)
	_ in.AdminOperator = (*AdminService)(nil)
	_ in.DeletionGuard = (*AdminService)(nil)
)
