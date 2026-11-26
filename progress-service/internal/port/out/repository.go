package out

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Repository is the read-side persistence port. All mutations go through Tx.
type Repository interface {
	GetCurrentProgress(ctx context.Context, accountID uuid.UUID) (*ProgressRecord, error)
	ListProgress(ctx context.Context, accountID uuid.UUID) ([]ProgressRecord, error)
	GetProgressByProgram(ctx context.Context, accountID, programID uuid.UUID) (*ProgressRecord, error)
	GetAccessibleLesson(ctx context.Context, accountID uuid.UUID) (*AccessibleLessonRecord, error)
	ListAccessibleLessons(ctx context.Context, accountID uuid.UUID) ([]AccessibleLessonRecord, error)
	ListPrograms(ctx context.Context, accountID uuid.UUID) ([]ActiveProgramRecord, error)
	GetActiveProgram(ctx context.Context, accountID uuid.UUID) (*ActiveProgramRecord, error)
	ListProgressByProgram(ctx context.Context, programID uuid.UUID) ([]ProgressRecord, error)
	HasCompletion(ctx context.Context, accountID, lessonID uuid.UUID) (bool, error)
	GetAccountTariff(ctx context.Context, accountID uuid.UUID) (*TariffSnapshotRecord, error)
	CountAnswers(ctx context.Context, accountID, lessonID uuid.UUID) (int, error)
	ListAnswers(ctx context.Context, f AnswerFilter) (AnswerPage, error)
	CountActivePatients(ctx context.Context, programID uuid.UUID, version int) (ActivePatients, error)
	FetchUnsentOutbox(ctx context.Context, limit int) ([]OutboxEvent, error)
	MarkOutboxSent(ctx context.Context, ids []uuid.UUID) error
	ActiveProgramCount(ctx context.Context) (int, error)
	Ping(ctx context.Context) error
}

// Tx exposes the transactional writes needed by BC#4 use cases.
type Tx interface {
	GetProgressForUpdate(ctx context.Context, accountID uuid.UUID) (*ProgressRecord, error)
	GetProgressByProgramForUpdate(ctx context.Context, accountID, programID uuid.UUID) (*ProgressRecord, error)
	ListProgress(ctx context.Context, accountID uuid.UUID) ([]ProgressRecord, error)
	ListProgressByProgramForUpdate(ctx context.Context, programID uuid.UUID) ([]ProgressRecord, error)
	InsertProgress(ctx context.Context, rec ProgressRecord) error
	UpdateProgress(ctx context.Context, rec ProgressRecord) error

	GetActiveProgramForUpdate(ctx context.Context, accountID uuid.UUID) (*ActiveProgramRecord, error)
	InsertActiveProgram(ctx context.Context, rec ActiveProgramRecord) error
	UpdateActiveProgram(ctx context.Context, rec ActiveProgramRecord) error
	ListPrograms(ctx context.Context, accountID uuid.UUID) ([]ActiveProgramRecord, error)

	GetAccessibleLessonForUpdate(ctx context.Context, accountID uuid.UUID) (*AccessibleLessonRecord, error)
	InsertAccessibleLesson(ctx context.Context, rec AccessibleLessonRecord) error
	RevokeAccessibleLessons(ctx context.Context, accountID uuid.UUID, at time.Time) (int64, error)

	HasCompletion(ctx context.Context, accountID, lessonID uuid.UUID) (bool, error)
	CountAnswers(ctx context.Context, accountID, lessonID uuid.UUID) (int, error)
	InsertCompletion(ctx context.Context, rec CompletionRecord) error
	InsertAnswer(ctx context.Context, rec AnswerRecord) error
	InsertSnapshot(ctx context.Context, rec SnapshotRecord) error

	InsertOutbox(ctx context.Context, ev OutboxEvent) error
	RecordIdempotency(ctx context.Context, rec IdempotencyRecord) (bool, error)

	GetAccountTariff(ctx context.Context, accountID uuid.UUID) (*TariffSnapshotRecord, error)
	UpsertAccountTariff(ctx context.Context, accountID uuid.UUID, rec *TariffSnapshotRecord) error

	DeleteAggregate(ctx context.Context, accountID uuid.UUID) error
	AnonymiseAnswers(ctx context.Context, accountID uuid.UUID, replacement string, at time.Time) error
}

// UnitOfWork runs a function inside one database transaction.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}
