package usecase

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Reddetk/SayMoDev/billing-service/internal/domain"
	"github.com/Reddetk/SayMoDev/billing-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/billing-service/internal/port/out"
)

func TestWebhookSignature_Deterministic(t *testing.T) {
	input := in.WebhookInput{
		BatchTimestamp: 1700000000,
		CurrencyID:     "BYN",
		Amount:         "29.90",
		PaymentMethod:  "cc",
		OrderID:        "order-1",
		SiteOrderID:    "payment-1",
		TransactionID:  "tx-1",
		PaymentType:    1,
		RRN:            "123",
		Card:           "5160**9495",
	}
	first := webhookSignature(input, "secret")
	second := webhookSignature(input, "secret")
	assert.Equal(t, first, second)
	assert.Len(t, first, 32)
	assert.NotEqual(t, first, webhookSignature(input, "other-secret"))
}

func TestDecimalToKopecks(t *testing.T) {
	valid := map[string]int64{
		"0":     0,
		"1":     100,
		"29.90": 2990,
		"29.9":  2990,
		"29.99": 2999,
		"0.01":  1,
	}
	for raw, want := range valid {
		got, err := decimalToKopecks(raw)
		require.NoError(t, err, raw)
		assert.Equal(t, want, got, raw)
	}
	for _, raw := range []string{"", ".5", "1.234", "abc", "1e5", "-1"} {
		_, err := decimalToKopecks(raw)
		assert.Error(t, err, raw)
	}
}

func TestMapDomainError(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{domain.ErrNotUpgrade, CodeNotUpgrade},
		{domain.ErrPeriodExpired, CodePeriodEnded},
		{domain.ErrPaymentRequired, CodePaymentFailed},
		{domain.ErrInvalidTransition, CodeInvalidInput},
	}
	for _, tc := range cases {
		mapped := mapDomainError(tc.err)
		var appErr *Error
		require.True(t, errors.As(mapped, &appErr), tc.err.Error())
		assert.Equal(t, tc.code, appErr.Code)
	}
}

func TestQuotePreview(t *testing.T) {
	quote := &out.QuoteSnapshot{
		QuoteID:             "q1",
		CurrentTariffPlanID: "plan-old",
		TargetTariffPlanID:  "plan-new",
		ChargeKopecks:       300,
		Currency:            "BYN",
		BillingDays:         30,
		RemainingDays:       10,
		OldPriceKopecks:     2990,
		NewPriceKopecks:     5990,
		ExpiresAt:           time.Now().Add(time.Minute),
	}
	preview := quotePreview(quote)
	assert.Equal(t, "plan-old", preview.CurrentTariffPlanID)
	assert.Equal(t, "plan-new", preview.TargetTariffPlanID)
	assert.EqualValues(t, 300, preview.Proration.AmountKopecks)
	assert.EqualValues(t, 200, preview.Proration.NewDailyRate)
	assert.EqualValues(t, 100, preview.Proration.OldDailyRate)
}

func TestSubscriptionRecordRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	period, err := domain.NewBillingPeriod(now, now.Add(30*24*time.Hour))
	require.NoError(t, err)
	snapshot, err := domain.NewTariffSnapshot(31, "Europe/Minsk")
	require.NoError(t, err)
	sub, err := domain.NewSubscription(domain.UUID{1}, domain.UUID{2}, domain.UUID{3}, domain.UUID{4}, snapshot, period, now)
	require.NoError(t, err)
	sub.Scheduled = &domain.ScheduledChange{TargetTariffPlanID: domain.UUID{5}, EffectiveAt: period.End}

	record := subscriptionRecord(sub)
	restored, err := subscriptionFromRecord(&record)
	require.NoError(t, err)

	assert.Equal(t, sub.ID, restored.ID)
	assert.Equal(t, sub.AccountID, restored.AccountID)
	assert.Equal(t, sub.Status, restored.Status)
	assert.Equal(t, sub.Snapshot, restored.Snapshot)
	assert.Equal(t, sub.Period.Start, restored.Period.Start)
	require.NotNil(t, restored.Scheduled)
	assert.Equal(t, sub.Scheduled.TargetTariffPlanID, restored.Scheduled.TargetTariffPlanID)
}

func TestSubscriptionFromRecord_Invalid(t *testing.T) {
	_, err := subscriptionFromRecord(&out.SubscriptionRecord{
		PeriodStart: time.Now(), PeriodEnd: time.Now().Add(-time.Hour),
		WeekBitmap: 127, Timezone: "UTC",
	})
	require.Error(t, err)

	_, err = subscriptionFromRecord(&out.SubscriptionRecord{
		PeriodStart: time.Now(), PeriodEnd: time.Now().Add(time.Hour),
		WeekBitmap: 127, Timezone: "Mars/Olympus",
	})
	require.Error(t, err)
}
