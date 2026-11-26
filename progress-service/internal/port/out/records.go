// Package out declares secondary ports and their DTOs (ADR-007: DTOs belong to the port package).
package out

import (
	"time"

	"github.com/google/uuid"
)

// TariffSnapshotRecord is the denormalised tariff copy stored with PatientProgress.
type TariffSnapshotRecord struct {
	WeekBitmap  int
	Timezone    string
	ValidUntil  *time.Time
	ActivatedAt time.Time
}

// ProgressRecord is one patient_progress row (one per accountId+programId).
// Domain value objects never cross this boundary: statuses are plain strings.
type ProgressRecord struct {
	AccountID         uuid.UUID
	ProgramID         uuid.UUID
	ProgramVersion    int
	Status            string
	CompletedLessons  int
	TotalLessons      int
	CompletionPercent int
	Tariff            *TariffSnapshotRecord
	LastCompletedAt   *time.Time
	LastUpdatedAt     time.Time
}

// ActiveProgramRecord is one active_programs row; paused rows are history.
type ActiveProgramRecord struct {
	AccountID         uuid.UUID
	ProgramID         uuid.UUID
	Version           int
	Status            string
	Difficulty        string
	AphasiaType       string
	ActivatedAt       time.Time
	CompletedAt       *time.Time
	PausedAt          *time.Time
	CompletionPercent int
}

// AccessibleLessonRecord is one accessible_lessons row.
type AccessibleLessonRecord struct {
	AccountID     uuid.UUID
	LessonID      uuid.UUID
	ProgramID     uuid.UUID
	LessonType    string
	OrderPosition int
	GrantedAt     time.Time
	RevokedAt     *time.Time
}

// AnswerRecord is an append-only lesson_answers row.
type AnswerRecord struct {
	AnswerID    uuid.UUID
	AccountID   uuid.UUID
	LessonID    uuid.UUID
	ProgramID   uuid.UUID
	MediaType   string
	Payload     string
	SubmittedAt time.Time
	Anonymised  bool
}

// CompletionRecord is a lesson_completions row.
type CompletionRecord struct {
	AccountID   uuid.UUID
	LessonID    uuid.UUID
	ProgramID   uuid.UUID
	CompletedAt time.Time
}

// SnapshotRecord is a progress_snapshots row.
type SnapshotRecord struct {
	AccountID         uuid.UUID
	ProgramID         uuid.UUID
	CompletionPercent int
	Reason            string
	CreatedAt         time.Time
}

// IdempotencyRecord guards Kafka consumers against at-least-once duplicates.
type IdempotencyRecord struct {
	EventID   string
	ExpiresAt time.Time
}

// OutboxEvent is the transactional outbox row (published asynchronously).
type OutboxEvent struct {
	ID            uuid.UUID
	EventID       string
	EventType     string
	EventVersion  int
	CorrelationID string
	Payload       []byte
	OccurredAt    time.Time
	SentAt        *time.Time
}

// LessonInfo is a denormalised lesson descriptor received from BC#3.
type LessonInfo struct {
	LessonID      uuid.UUID
	OrderPosition int
	LessonType    string
	Title         string
}

// ProgramInfo is a BC#3 program catalog entry.
type ProgramInfo struct {
	ProgramID    uuid.UUID
	Version      int
	TotalLessons int
	Difficulty   string
	AphasiaType  string
	Lessons      []LessonInfo
}

// AnswerFilter narrows admin answer queries.
type AnswerFilter struct {
	AccountID uuid.UUID
	LessonID  *uuid.UUID
	MediaType string
	Limit     int
	Offset    int
	Ascending bool
}

// AnswerPage is a paginated answer list with the total count.
type AnswerPage struct {
	Answers []AnswerRecord
	Total   int
}

// ActivePatients is the deletion guard projection for BC#3.
type ActivePatients struct {
	ProgramID uuid.UUID
	Version   int
	Count     int
	IDs       []uuid.UUID
}
