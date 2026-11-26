// Package in declares primary ports and their DTOs (ADR-007: DTOs belong to the port package).
package in

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// AuthContext is the verified identity extracted from a JWT.
type AuthContext struct {
	AccountID string
	Role      string
	SessionID string
	Rev       int64
}

// TokenValidator verifies RS256 JWTs and the shared revocation projection.
type TokenValidator interface {
	ValidateToken(ctx context.Context, rawToken string) (AuthContext, error)
}

// --- GET /progress/me ---

type TariffSnapshotView struct {
	WeekBitmap  int        `json:"weekBitmap"`
	Timezone    string     `json:"timezone"`
	ValidUntil  *time.Time `json:"validUntil"`
	ActivatedAt time.Time  `json:"activatedAt"`
}

type ActiveProgramView struct {
	ProgramID         string     `json:"programId"`
	Status            string     `json:"status"`
	ActivatedAt       time.Time  `json:"activatedAt"`
	CompletedAt       *time.Time `json:"completedAt"`
	PausedAt          *time.Time `json:"pausedAt"`
	CompletionPercent int        `json:"completionPercent"`
}

type AccessibleLessonView struct {
	LessonID       string     `json:"lessonId"`
	LessonType     string     `json:"lessonType"`
	NextUnlockDate *time.Time `json:"nextUnlockDate"`
	UnlockReason   string     `json:"unlockReason"`
}

type ProgressView struct {
	AccountID         string                `json:"accountId"`
	ProgramID         string                `json:"programId"`
	Status            string                `json:"status"`
	CompletedLessons  int                   `json:"completedLessons"`
	TotalLessons      int                   `json:"totalLessons"`
	CompletionPercent int                   `json:"completionPercent"`
	ActiveProgram     *ActiveProgramView    `json:"activeProgram"`
	TariffSnapshot    *TariffSnapshotView   `json:"tariffSnapshot"`
	AccessibleLesson  *AccessibleLessonView `json:"accessibleLesson"`
	LastUpdatedAt     time.Time             `json:"lastUpdatedAt"`
}

// --- POST /progress/me/activate ---

type ActivateCommand struct {
	AccountID     uuid.UUID
	ProgramID     *uuid.UUID
	Difficulty    string
	AphasiaType   string
	HasClassifier bool
}

type GrantedLessonView struct {
	LessonID      string `json:"lessonId"`
	OrderPosition int    `json:"orderPosition"`
}

type ActivateResult struct {
	AccountID         string             `json:"accountId"`
	ProgramID         string             `json:"programId"`
	Status            string             `json:"status"`
	CompletionPercent int                `json:"completionPercent"`
	TotalLessons      int                `json:"totalLessons"`
	AccessibleLesson  *GrantedLessonView `json:"accessibleLesson"`
}

// --- DELETE /progress/me ---

type DeleteCommand struct {
	AccountID uuid.UUID
	Confirm   string
}

// --- answers ---

type PresignCommand struct {
	AccountID   uuid.UUID
	LessonID    uuid.UUID
	MediaType   string
	ContentType string
}

type PresignResult struct {
	PresignedPutURL string    `json:"presignedPutUrl"`
	ObjectKey       string    `json:"objectKey"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

type SubmitAnswerCommand struct {
	AccountID uuid.UUID
	LessonID  uuid.UUID
	MediaType string
	Payload   string
}

type SubmitAnswerResult struct {
	AnswerID    string    `json:"answerId"`
	SubmittedAt time.Time `json:"submittedAt"`
	Message     string    `json:"message"`
}

// --- POST /progress/lessons/{id}/complete ---

type CompleteCommand struct {
	AccountID uuid.UUID
	LessonID  uuid.UUID
}

type CompleteResult struct {
	CompletionPercent int     `json:"completionPercent"`
	NextLessonID      *string `json:"nextLessonId"`
}

// --- difficulty ---

type ChangeDifficultyCommand struct {
	AccountID     uuid.UUID
	NewDifficulty string
	AphasiaType   string
}

type ChangeDifficultyResult struct {
	OldProgramID         string `json:"oldProgramId"`
	NewProgramID         string `json:"newProgramId"`
	OldCompletionPercent int    `json:"oldCompletionPercent"`
	NewCompletionPercent int    `json:"newCompletionPercent"`
	Message              string `json:"message,omitempty"`
}

// --- resume ---

type ResumeCommand struct {
	AccountID uuid.UUID
	ProgramID uuid.UUID
}

type ResumeResult struct {
	ProgramID         string             `json:"programId"`
	Status            string             `json:"status"`
	CompletionPercent int                `json:"completionPercent"`
	AccessibleLesson  *GrantedLessonView `json:"accessibleLesson"`
	Message           string             `json:"message"`
}

// --- admin ---

type AdminAccessibleLessonView struct {
	LessonID  string     `json:"lessonId"`
	RevokedAt *time.Time `json:"revokedAt"`
}

type PausedProgramView struct {
	ProgramID         string     `json:"programId"`
	Status            string     `json:"status"`
	ActivatedAt       time.Time  `json:"activatedAt"`
	PausedAt          *time.Time `json:"pausedAt"`
	CompletionPercent int        `json:"completionPercent"`
}

type AdminProgressView struct {
	AccountID         string                      `json:"accountId"`
	Email             string                      `json:"email"`
	ProgramID         string                      `json:"programId"`
	Status            string                      `json:"status"`
	CompletedLessons  int                         `json:"completedLessons"`
	TotalLessons      int                         `json:"totalLessons"`
	CompletionPercent int                         `json:"completionPercent"`
	TariffSnapshot    *TariffSnapshotView         `json:"tariffSnapshot"`
	ActiveProgram     *ActiveProgramView          `json:"activeProgram"`
	AccessibleLessons []AdminAccessibleLessonView `json:"accessibleLessons"`
	PausedPrograms    []PausedProgramView         `json:"pausedPrograms"`
	LastUpdatedAt     time.Time                   `json:"lastUpdatedAt"`
}

type AdminAnswerView struct {
	AnswerID    string    `json:"answerId"`
	LessonID    string    `json:"lessonId"`
	LessonTitle string    `json:"lessonTitle"`
	Payload     string    `json:"payload"`
	MediaType   string    `json:"mediaType"`
	SubmittedAt time.Time `json:"submittedAt"`
}

type AdminAnswersPage struct {
	Answers []AdminAnswerView `json:"answers"`
	Total   int               `json:"total"`
	Limit   int               `json:"limit"`
	Offset  int               `json:"offset"`
}

type AdminAnswersQuery struct {
	AccountID uuid.UUID
	LessonID  *uuid.UUID
	MediaType string
	Limit     int
	Offset    int
}

type ActivePatientsView struct {
	ProgramID          string   `json:"programId"`
	Version            int      `json:"version"`
	ActivePatientCount int      `json:"activePatientCount"`
	PatientIDs         []string `json:"patientIds"`
}

// PatientPorts groups the patient-facing use cases.
type ProgressReader interface {
	GetMyProgress(ctx context.Context, accountID uuid.UUID) (ProgressView, error)
}

type ProgramOperator interface {
	Activate(ctx context.Context, cmd ActivateCommand) (ActivateResult, error)
	ChangeDifficulty(ctx context.Context, cmd ChangeDifficultyCommand) (ChangeDifficultyResult, error)
	Resume(ctx context.Context, cmd ResumeCommand) (ResumeResult, error)
}

type LessonAnswerer interface {
	Presign(ctx context.Context, cmd PresignCommand) (PresignResult, error)
	Submit(ctx context.Context, cmd SubmitAnswerCommand) (SubmitAnswerResult, error)
}

type LessonCompleter interface {
	Complete(ctx context.Context, cmd CompleteCommand) (CompleteResult, error)
}

type SelfDeleter interface {
	DeleteMyProgress(ctx context.Context, cmd DeleteCommand) error
}

type AdminReader interface {
	GetProgress(ctx context.Context, accountID uuid.UUID) (AdminProgressView, error)
	ListAnswers(ctx context.Context, q AdminAnswersQuery) (AdminAnswersPage, error)
	ListLessonAnswers(ctx context.Context, accountID, lessonID uuid.UUID) ([]AdminAnswerView, error)
}

type AdminOperator interface {
	ChangeDifficulty(ctx context.Context, cmd ChangeDifficultyCommand) (ChangeDifficultyResult, error)
}

type DeletionGuard interface {
	ActivePatients(ctx context.Context, programID uuid.UUID, version int) (ActivePatientsView, error)
}

// --- event-driven use cases (Kafka consumers) ---

type RegistrationEvent struct {
	AccountID     string
	Role          string
	Difficulty    string
	AphasiaType   string
	HasClassifier bool
	EventID       string
}

type TariffEvent struct {
	EventID     string
	AccountID   uuid.UUID
	EventType   string
	WeekBitmap  int
	Timezone    string
	ValidUntil  *time.Time
	HasSnapshot bool
}

type MigrationEvent struct {
	EventID       string
	JobID         string
	ProgramID     uuid.UUID
	TargetVersion int
	AccountIDs    []uuid.UUID
}

type LessonAddedEvent struct {
	EventID         string
	ProgramID       uuid.UUID
	LessonID        uuid.UUID
	Position        int
	LessonType      string
	NewTotalLessons int
}

// AccountDeletedEvent triggers GDPR cleanup of BC#4 data (BC#1 canonical deletion).
type AccountDeletedEvent struct {
	EventID   string
	AccountID uuid.UUID
}

// EventSink is implemented by the core services and called by Kafka adapters.
type EventSink interface {
	OnAccountRegistered(ctx context.Context, ev RegistrationEvent) error
	OnAccountDeleted(ctx context.Context, ev AccountDeletedEvent) error
	OnTariffChanged(ctx context.Context, ev TariffEvent) error
	OnMigrationRequested(ctx context.Context, ev MigrationEvent) error
	OnLessonAdded(ctx context.Context, ev LessonAddedEvent) error
}
