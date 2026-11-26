package domain

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePrice_FromFixtures(t *testing.T) {
	for _, row := range readFixture(t, "money_cases.csv") {
		row := row
		t.Run(row["case_id"], func(t *testing.T) {
			money, err := ParsePrice(row["amount_kopecks"], row["currency"])

			if row["want_error"] == "true" {
				require.Error(t, err, row["description"])
				assert.Contains(t, strings.ToLower(err.Error()), row["want_error_contains"], row["description"])
				return
			}
			require.NoError(t, err, row["description"])
			assert.Equal(t, mustInt64(t, row["want_amount"]), money.AmountKopecks)
			assert.Equal(t, row["want_currency"], string(money.Currency))
		})
	}
}

func TestParseWeekBitmap_FromFixtures(t *testing.T) {
	for _, row := range readFixture(t, "bitmap_cases.csv") {
		row := row
		t.Run(row["case_id"], func(t *testing.T) {
			bitmap, err := ParseWeekBitmap(row["bitmap"])

			if row["want_error"] == "true" {
				require.Error(t, err, row["description"])
				assert.Contains(t, strings.ToLower(err.Error()), row["want_error_contains"], row["description"])
				return
			}
			require.NoError(t, err, row["description"])
			assert.Equal(t, int(mustInt64(t, row["want_bitmap"])), int(bitmap))
		})
	}
}

func TestCalculateProration_FromFixtures(t *testing.T) {
	for _, row := range readFixture(t, "proration_cases.csv") {
		row := row
		t.Run(row["case_id"], func(t *testing.T) {
			charge, err := CalculateProrationFromStrings(
				row["old_price_kopecks"],
				row["new_price_kopecks"],
				row["billing_days"],
				row["remaining_days"],
			)

			if row["want_error"] == "true" {
				require.Error(t, err, row["description"])
				assert.Contains(t, strings.ToLower(err.Error()), row["want_error_contains"], row["description"])
				return
			}
			require.NoError(t, err, row["description"])
			assert.Equal(t, mustInt64(t, row["want_charge_kopecks"]), charge.AmountKopecks)
			assert.Equal(t, CurrencyBYN, charge.Currency)
		})
	}
}

func TestSubscriptionTransitions_FromFixtures(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	plan := UUID{1}
	offer := UUID{2}
	targetPlan := UUID{3}
	targetOffer := UUID{4}
	snapshot, err := NewTariffSnapshot(127, "UTC")
	require.NoError(t, err)

	for _, row := range readFixture(t, "transition_cases.csv") {
		row := row
		t.Run(row["case_id"], func(t *testing.T) {
			start, end := now.Add(-24*time.Hour), now.Add(29*24*time.Hour)
			if row["period_expired"] == "true" {
				start, end = now.Add(-30*24*time.Hour), now.Add(-24*time.Hour)
			}
			period, err := NewBillingPeriod(start, end)
			require.NoError(t, err)

			sub, err := NewSubscription(UUID{10}, UUID{11}, plan, offer, snapshot, period, now)
			require.NoError(t, err)
			sub.Status = SubscriptionStatus(row["current_status"])

			paymentSuccess := row["payment_success"] == "true"
			switch row["action"] {
			case "activate":
				err = sub.Activate(paymentSuccess, now)
			case "suspend":
				err = sub.Suspend(now)
			case "resume":
				err = sub.Resume(now)
			case "cancel":
				err = sub.Cancel(now)
			case "expire":
				err = sub.Expire(now)
			case "upgrade":
				err = sub.ApplyUpgrade(targetPlan, targetOffer, snapshot, now)
			case "downgrade_at_period_end":
				err = sub.ScheduleDowngrade(targetPlan, now.Add(time.Hour), now)
			default:
				t.Fatalf("unknown action %q", row["action"])
			}

			if row["want_error"] == "true" {
				require.Error(t, err, row["description"])
				assert.Contains(t, strings.ToLower(err.Error()), row["want_error_contains"], row["description"])
				return
			}
			require.NoError(t, err, row["description"])
			assert.Equal(t, row["want_status"], string(sub.Status), row["description"])
		})
	}
}

func TestMoney_AddOverflow(t *testing.T) {
	max, err := NewPaymentAmount(MaxAmountKopecks, CurrencyBYN)
	require.NoError(t, err)

	_, err = max.Add(Money{AmountKopecks: 1, Currency: CurrencyBYN})
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "range")

	_, err = max.Add(Money{AmountKopecks: -1, Currency: CurrencyUSD})
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "currency")
}

func TestTariffSnapshot_Validation(t *testing.T) {
	_, err := NewTariffSnapshot(127, "Mars/Olympus")
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "timezone")

	_, err = NewTariffSnapshot(127, "")
	require.Error(t, err)

	snapshot, err := NewTariffSnapshot(0, "Europe/Minsk")
	require.NoError(t, err)
	assert.Equal(t, WeekBitmap(0), snapshot.WeekBitmap)
}

func TestBillingPeriod_RemainingAndExpiry(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(30 * 24 * time.Hour)
	period, err := NewBillingPeriod(start, end)
	require.NoError(t, err)

	assert.Equal(t, 30, period.RemainingDays(start))
	assert.Equal(t, 1, period.RemainingDays(end.Add(-time.Hour)))
	assert.Equal(t, 0, period.RemainingDays(end))
	assert.True(t, period.Expired(end))
	assert.False(t, period.Expired(end.Add(-time.Minute)))
}

func readFixture(t *testing.T, name string) []map[string]string {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "fixtures", name)
	f, err := os.Open(path)
	require.NoError(t, err, path)
	defer func() { _ = f.Close() }()

	reader := csv.NewReader(f)
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(records), 2)

	header := records[0]
	rows := make([]map[string]string, 0, len(records)-1)
	for _, record := range records[1:] {
		if len(record) == 0 {
			continue
		}
		// Независимый автор оставил лишнюю пустую колонку в error-строках —
		// допускаем один trailing empty сверх заголовка.
		for len(record) > len(header) && record[len(record)-1] == "" {
			record = record[:len(record)-1]
		}
		require.Len(t, record, len(header), "malformed CSV row: %v", record)
		row := make(map[string]string, len(header))
		for i, key := range header {
			row[key] = record[i]
		}
		rows = append(rows, row)
	}
	return rows
}

func mustInt64(t *testing.T, raw string) int64 {
	t.Helper()
	if raw == "" {
		return 0
	}
	var value int64
	for _, r := range raw {
		require.True(t, r >= '0' && r <= '9', "not an integer: %q", raw)
		value = value*10 + int64(r-'0')
	}
	return value
}
