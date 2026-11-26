// Package entity holds BC#4 aggregates and entities.
package entity

import (
	"time"

	"github.com/google/uuid"

	"github.com/Reddetk/SayMoDev/progress-service/internal/core/valobj"
)

// PatientProgress is the BC#4 aggregate root. One row per (accountId, programId):
// paused programs keep their frozen progress for history.
type PatientProgress struct {
	AccountID         uuid.UUID
	ProgramID         uuid.UUID
	ProgramVersion    int
	Status            valobj.ProgramStatus
	CompletedLessons  int
	TotalLessons      int
	CompletionPercent valobj.CompletionPercent
	Tariff            *valobj.TariffSnapshot
	LastCompletedAt   *time.Time
	LastUpdatedAt     time.Time
}

// Recalculate recomputes completionPercent from completed/total lessons.
func (p *PatientProgress) Recalculate() {
	p.CompletionPercent = valobj.CalculatePercent(p.CompletedLessons, p.TotalLessons)
}

// HasActiveTariff reports whether unlock calculations may grant lessons (Rule 3).
func (p *PatientProgress) HasActiveTariff(now time.Time) bool {
	return p.Tariff != nil && !p.Tariff.Expired(now)
}

// Unlock answers whether the currently granted lesson is available right now.
// The granted row itself exists in accessible_lessons; availability is lazy — invariant 9.
func (p *PatientProgress) Unlock(now time.Time) (available bool, next *time.Time, reason string) {
	if !p.HasActiveTariff(now) {
		return false, nil, ""
	}
	if p.Tariff.WeekBitmap <= 0 {
		// Bitmap 0 disables every weekday: nothing may unlock (Rule 3 spirit).
		return false, nil, ""
	}
	if p.LastCompletedAt == nil {
		// First lesson of a program is available as soon as the program has a tariff
		// with at least one enabled day.
		return true, nil, "immediate"
	}
	at, scheduled, ok := valobj.NextUnlockFrom(*p.LastCompletedAt, p.Tariff.WeekBitmap, p.Tariff.Timezone)
	if !ok {
		return false, nil, ""
	}
	if !at.After(now) {
		return true, nil, "immediate"
	}
	reason = "immediate"
	if scheduled {
		reason = "scheduled"
	}
	return false, &at, reason
}

// CompleteLesson advances progress irreversibly and freezes lastCompletedAt.
func (p *PatientProgress) CompleteLesson(now time.Time) {
	if p.CompletedLessons < p.TotalLessons {
		p.CompletedLessons++
	}
	p.Recalculate()
	t := now.UTC()
	p.LastCompletedAt = &t
	p.LastUpdatedAt = t
}

// Finished reports whether the program reached 100%.
func (p *PatientProgress) Finished() bool {
	return p.CompletionPercent >= 100
}

// SwitchToProgram prepares this progress row for a newly activated program (difficulty change).
func (p *PatientProgress) SwitchToProgram(programID uuid.UUID, version, totalLessons int, now time.Time) {
	p.ProgramID = programID
	p.ProgramVersion = version
	p.TotalLessons = totalLessons
	p.CompletedLessons = 0
	p.CompletionPercent = 0
	p.Status = valobj.ProgramStatusActive
	p.LastCompletedAt = nil
	p.LastUpdatedAt = now.UTC()
}

// ApplyTariff updates the denormalised tariff snapshot.
func (p *PatientProgress) ApplyTariff(snap *valobj.TariffSnapshot, now time.Time) {
	p.Tariff = snap
	p.LastUpdatedAt = now.UTC()
}

// ActiveProgram is an entity with history: max one active, many paused/completed.
type ActiveProgram struct {
	AccountID         uuid.UUID
	ProgramID         uuid.UUID
	Version           int
	Status            valobj.ProgramStatus
	Difficulty        valobj.Difficulty
	AphasiaType       string
	ActivatedAt       time.Time
	CompletedAt       *time.Time
	PausedAt          *time.Time
	CompletionPercent valobj.CompletionPercent
}

// AccessibleLesson is the single currently granted lesson (sequential access).
type AccessibleLesson struct {
	AccountID     uuid.UUID
	LessonID      uuid.UUID
	ProgramID     uuid.UUID
	LessonType    valobj.LessonType
	OrderPosition int
	GrantedAt     time.Time
	RevokedAt     *time.Time
}

// IsActive reports whether the grant is still in force.
func (a AccessibleLesson) IsActive() bool { return a.RevokedAt == nil }

// LessonAnswer is an append-only entity; never updated, never deleted.
type LessonAnswer struct {
	AnswerID    uuid.UUID
	AccountID   uuid.UUID
	LessonID    uuid.UUID
	ProgramID   uuid.UUID
	MediaType   valobj.MediaType
	Payload     string
	SubmittedAt time.Time
	Anonymised  bool
}

// ProgressSnapshot freezes progress at a point in time.
type ProgressSnapshot struct {
	AccountID         uuid.UUID
	ProgramID         uuid.UUID
	CompletionPercent valobj.CompletionPercent
	Reason            valobj.SnapshotReason
	CreatedAt         time.Time
}
