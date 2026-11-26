package valobj

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoError(t, err)
	return loc
}

func TestCalculatePercent(t *testing.T) {
	cases := []struct{ completed, total, want int }{
		{0, 3, 0}, {1, 3, 33}, {2, 3, 66}, {3, 3, 100},
		{0, 0, 0}, {-1, 3, 0}, {4, 3, 100},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, int(CalculatePercent(c.completed, c.total)), "completed=%d total=%d", c.completed, c.total)
	}
}

func TestNextUnlockRule1SameDay(t *testing.T) {
	loc := mustLoc(t, "Europe/Moscow")
	monday := time.Date(2026, 3, 2, 15, 0, 0, 0, loc) // Monday, bit 0
	at, scheduled, ok := NextUnlockFrom(monday, 0b0000001, "Europe/Moscow")
	require.True(t, ok)
	assert.False(t, scheduled)
	assert.True(t, at.Equal(monday))
}

func TestNextUnlockRule2NextEnabledDay(t *testing.T) {
	loc := mustLoc(t, "Europe/Moscow")
	monday := time.Date(2026, 3, 2, 15, 0, 0, 0, loc)
	// Only Wednesday (bit 2) is enabled.
	at, scheduled, ok := NextUnlockFrom(monday, 0b0000100, "Europe/Moscow")
	require.True(t, ok)
	assert.True(t, scheduled)
	want := time.Date(2026, 3, 4, 0, 0, 0, 0, loc)
	assert.True(t, at.Equal(want), "got %s want %s", at, want)
	assert.Equal(t, time.Wednesday, at.In(loc).Weekday())
}

func TestNextUnlockDisabledBitmap(t *testing.T) {
	_, _, ok := NextUnlockFrom(time.Now(), 0, "UTC")
	assert.False(t, ok)
}

func TestNextUnlockUnknownTimezoneFallsBackToUTC(t *testing.T) {
	now := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	at, _, ok := NextUnlockFrom(now, 0b0000001, "Not/AZone")
	require.True(t, ok)
	assert.True(t, at.Equal(now))
}

func TestNextUnlockAcrossDST(t *testing.T) {
	loc := mustLoc(t, "America/New_York")
	// Saturday 2026-03-07, only Sunday (bit 6) enabled; DST starts Sunday 2026-03-08.
	saturday := time.Date(2026, 3, 7, 12, 0, 0, 0, loc)
	at, scheduled, ok := NextUnlockFrom(saturday, 0b1000000, "America/New_York")
	require.True(t, ok)
	assert.True(t, scheduled)
	assert.Equal(t, time.Sunday, at.In(loc).Weekday())
	assert.Equal(t, 0, at.In(loc).Hour())
}

func TestTariffExpired(t *testing.T) {
	now := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	future := now.Add(time.Minute)

	assert.False(t, TariffSnapshot{}.Expired(now), "nil validUntil is indefinite")
	assert.True(t, TariffSnapshot{ValidUntil: &past}.Expired(now))
	assert.False(t, TariffSnapshot{ValidUntil: &future}.Expired(now))
}

func TestDayAllowedMondayFirst(t *testing.T) {
	monday := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	assert.True(t, TariffSnapshot{WeekBitmap: 0b0000001, Timezone: "UTC"}.DayAllowed(monday))
	sunday := time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC)
	assert.True(t, TariffSnapshot{WeekBitmap: 0b1000000, Timezone: "UTC"}.DayAllowed(sunday))
	assert.False(t, TariffSnapshot{WeekBitmap: 0b0000001, Timezone: "UTC"}.DayAllowed(sunday))
}
