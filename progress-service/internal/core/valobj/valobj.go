// Package valobj contains immutable domain value objects of BC#4.
package valobj

import (
	"fmt"
	"math"
	"time"
)

// Difficulty is a classifier dimension owned by BC#3; BC#4 keeps it as a plain value.
type Difficulty string

const (
	DifficultyEasy   Difficulty = "easy"
	DifficultyMedium Difficulty = "medium"
	DifficultyHard   Difficulty = "hard"
)

// Valid reports whether d is a documented difficulty value.
func (d Difficulty) Valid() bool {
	switch d {
	case DifficultyEasy, DifficultyMedium, DifficultyHard:
		return true
	}
	return false
}

// LessonType distinguishes lessons that require an answer before completion.
type LessonType string

const (
	LessonTypeRegular  LessonType = "regular"
	LessonTypeCreative LessonType = "creative"
)

// Valid reports whether t is a documented lesson type.
func (t LessonType) Valid() bool {
	return t == LessonTypeRegular || t == LessonTypeCreative
}

// MediaType is the kind of a submitted lesson answer.
type MediaType string

const (
	MediaTypeText  MediaType = "text"
	MediaTypePhoto MediaType = "photo"
	MediaTypeVideo MediaType = "video"
)

// Valid reports whether m is a documented media type.
func (m MediaType) Valid() bool {
	switch m {
	case MediaTypeText, MediaTypePhoto, MediaTypeVideo:
		return true
	}
	return false
}

// ContentType whitelist for presigned uploads.
var allowedContentTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"video/mp4":  ".mp4",
}

// ExtForContentType returns the object extension for a whitelisted content type.
func ExtForContentType(contentType string) (string, bool) {
	ext, ok := allowedContentTypes[contentType]
	return ext, ok
}

// ProgramStatus is the lifecycle state of an ActiveProgram entity.
type ProgramStatus string

const (
	ProgramStatusActive    ProgramStatus = "active"
	ProgramStatusPaused    ProgramStatus = "paused"
	ProgramStatusCompleted ProgramStatus = "completed"
)

// Valid reports whether s is a documented program status.
func (s ProgramStatus) Valid() bool {
	switch s {
	case ProgramStatusActive, ProgramStatusPaused, ProgramStatusCompleted:
		return true
	}
	return false
}

// SnapshotReason enumerates progress_snapshot triggers.
type SnapshotReason string

const (
	SnapshotProgramCompleted  SnapshotReason = "program_completed"
	SnapshotDifficultyChanged SnapshotReason = "difficulty_changed"
	SnapshotTariffChanged     SnapshotReason = "tariff_changed"
)

// CompletionPercent is an integer 0-100 computed as FLOOR(completed*100/total).
type CompletionPercent int

// CalculatePercent implements the invariant from classifications/ProgressTrackingAndAssessment.md.
func CalculatePercent(completed, total int) CompletionPercent {
	if total <= 0 || completed < 0 {
		return 0
	}
	if completed > total {
		completed = total
	}
	return CompletionPercent(int(math.Floor(float64(completed) * 100.0 / float64(total))))
}

// TariffSnapshot is a denormalised copy of BC#2 tariff data owned by PatientProgress.
// validUntil == nil is a valid business state meaning "indefinite", never an error.
type TariffSnapshot struct {
	WeekBitmap  int
	Timezone    string
	ValidUntil  *time.Time
	ActivatedAt time.Time
}

// Expired reports whether the snapshot is time-bounded and already over.
func (t TariffSnapshot) Expired(now time.Time) bool {
	return t.ValidUntil != nil && !now.Before(*t.ValidUntil)
}

// Location resolves the patient timezone, falling back to UTC when unknown.
func (t TariffSnapshot) Location() *time.Location {
	if t.Timezone == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(t.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// DayAllowed reports whether the weekday of t (in patient timezone) is enabled in weekBitmap.
// Bit 0 = Monday ... bit 6 = Sunday.
func (t TariffSnapshot) DayAllowed(at time.Time) bool {
	idx := WeekdayIndex(at.In(t.Location()))
	return t.WeekBitmap&(1<<idx) != 0
}

// WeekdayIndex maps Go weekday (Sunday=0) to the documented Monday-first bit index.
func WeekdayIndex(t time.Time) int {
	return (int(t.Weekday()) + 6) % 7
}

// NextUnlockFrom implements the lesson unlock schedule (invariant 9) relative to the
// moment the previous lesson was completed:
//
//	Rule 1: completion day bit=1 -> next lesson unlocks immediately (at completedAt)
//	Rule 2: completion day bit=0 -> at 00:00 of the next day where bit=1, patient timezone
//
// ok=false means the bitmap has no enabled day at all, so no lesson may unlock.
func NextUnlockFrom(completedAt time.Time, weekBitmap int, timezone string) (at time.Time, scheduled bool, ok bool) {
	snap := TariffSnapshot{WeekBitmap: weekBitmap, Timezone: timezone}
	loc := snap.Location()
	base := completedAt.In(loc)

	if weekBitmap < 0 || weekBitmap > 127 || weekBitmap == 0 {
		return time.Time{}, false, false
	}
	if snap.DayAllowed(base) {
		return completedAt, false, true
	}

	midnight := time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, loc)
	for d := 1; d <= 7; d++ {
		candidate := midnight.AddDate(0, 0, d)
		if snap.WeekBitmap&(1<<WeekdayIndex(candidate)) != 0 {
			return candidate, true, true
		}
	}
	return time.Time{}, false, false
}

// ParseDifficulty validates and converts a request string.
func ParseDifficulty(raw string) (Difficulty, error) {
	d := Difficulty(raw)
	if !d.Valid() {
		return "", fmt.Errorf("difficulty %q: %w", raw, ErrInvalidDifficulty)
	}
	return d, nil
}

// ErrInvalidDifficulty is re-exported through coreErrors at the domain boundary.
var ErrInvalidDifficulty = fmt.Errorf("invalid_difficulty")
