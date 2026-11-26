package entity

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/Reddetk/SayMoDev/progress-service/internal/core/valobj"
)

func TestUnlockWithoutTariff(t *testing.T) {
	p := PatientProgress{TotalLessons: 3}
	available, next, reason := p.Unlock(time.Now())
	assert.False(t, available)
	assert.Nil(t, next)
	assert.Empty(t, reason)
}

func TestUnlockExpiredTariff(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	p := PatientProgress{Tariff: &valobj.TariffSnapshot{WeekBitmap: 127, Timezone: "UTC", ValidUntil: &past}}
	available, _, _ := p.Unlock(time.Now())
	assert.False(t, available)
}

func TestFirstLessonAvailableImmediately(t *testing.T) {
	p := PatientProgress{Tariff: &valobj.TariffSnapshot{WeekBitmap: 127, Timezone: "UTC"}}
	available, next, reason := p.Unlock(time.Now())
	assert.True(t, available)
	assert.Nil(t, next)
	assert.Equal(t, "immediate", reason)
}

func TestUnlockDisabledBitmapMeansNoAccess(t *testing.T) {
	p := PatientProgress{Tariff: &valobj.TariffSnapshot{WeekBitmap: 0, Timezone: "UTC"}}
	available, _, _ := p.Unlock(time.Now())
	assert.False(t, available)
}

func TestUnlockScheduledWhenTodayDisabled(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	monday := time.Date(2026, 3, 2, 15, 0, 0, 0, loc)
	lca := monday
	p := PatientProgress{
		TotalLessons:    3,
		LastCompletedAt: &lca,
		Tariff:          &valobj.TariffSnapshot{WeekBitmap: 0b0000100, Timezone: "Europe/Moscow"}, // Wed only
	}
	available, next, reason := p.Unlock(monday)
	assert.False(t, available)
	assert.NotNil(t, next)
	assert.Equal(t, "scheduled", reason)
	assert.Equal(t, time.Wednesday, next.In(loc).Weekday())
}

func TestUnlockUsesCompletionDayNotReadDay(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	monday := time.Date(2026, 3, 2, 15, 0, 0, 0, loc)
	completed := monday
	p := PatientProgress{
		TotalLessons:    3,
		LastCompletedAt: &completed,
		Tariff:          &valobj.TariffSnapshot{WeekBitmap: 0b0000001, Timezone: "Europe/Moscow"}, // Monday only
	}
	// Read two days later: the lesson unlocked on Monday and must stay available.
	wednesday := monday.AddDate(0, 0, 2)
	available, next, reason := p.Unlock(wednesday)
	assert.True(t, available)
	assert.Nil(t, next)
	assert.Equal(t, "immediate", reason)
}

func TestCompleteLessonAdvancesAndFloors(t *testing.T) {
	now := time.Now().UTC()
	p := PatientProgress{CompletedLessons: 0, TotalLessons: 3}
	p.CompleteLesson(now)
	assert.Equal(t, 1, p.CompletedLessons)
	assert.Equal(t, valobj.CompletionPercent(33), p.CompletionPercent)
	assert.NotNil(t, p.LastCompletedAt)

	p.CompletedLessons = 3
	p.CompleteLesson(now)
	assert.Equal(t, 3, p.CompletedLessons, "never exceeds total")
	assert.Equal(t, valobj.CompletionPercent(100), p.CompletionPercent)
	assert.True(t, p.Finished())
}

func TestSwitchToProgramResetsProgress(t *testing.T) {
	p := PatientProgress{CompletedLessons: 2, TotalLessons: 3, CompletionPercent: 66}
	newID := uuid.New()
	p.SwitchToProgram(newID, 2, 5, time.Now())
	assert.Equal(t, newID, p.ProgramID)
	assert.Equal(t, 0, p.CompletedLessons)
	assert.Equal(t, 0, int(p.CompletionPercent))
	assert.Equal(t, 5, p.TotalLessons)
	assert.Nil(t, p.LastCompletedAt)
}
