package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPayment_Lifecycle(t *testing.T) {
	amount, err := NewPaymentAmount(1000, CurrencyBYN)
	require.NoError(t, err)
	now := time.Now().UTC()

	payment, err := NewPayment(UUID{1}, amount, PaymentInitial, "Basic", now)
	require.NoError(t, err)
	assert.Equal(t, PaymentPending, payment.Status)
	assert.False(t, payment.Status.Final())

	require.NoError(t, payment.MarkSuccess("tx-1", now))
	assert.Equal(t, PaymentSuccess, payment.Status)
	assert.True(t, payment.Status.Final())
	require.NoError(t, payment.MarkSuccess("tx-1", now), "idempotent success")
	require.Error(t, payment.MarkFailed(now), "success is final")

	other, err := NewPayment(UUID{2}, amount, PaymentRenewal, "Basic", now)
	require.NoError(t, err)
	require.NoError(t, other.MarkFailed(now))
	assert.Equal(t, PaymentFailed, other.Status)
	require.NoError(t, other.MarkFailed(now), "idempotent failure")
	require.Error(t, other.MarkSuccess("tx-2", now), "failed is final")
}

func TestPayment_Validation(t *testing.T) {
	amount, _ := NewPaymentAmount(1000, CurrencyBYN)
	now := time.Now().UTC()

	_, err := NewPayment(UUID{}, amount, PaymentInitial, "Basic", now)
	require.Error(t, err)

	_, err = NewPayment(UUID{1}, amount, PaymentKind("bogus"), "Basic", now)
	require.Error(t, err)

	badAmount := Money{AmountKopecks: 0, Currency: CurrencyBYN}
	_, err = NewPayment(UUID{1}, badAmount, PaymentInitial, "Basic", now)
	require.Error(t, err)

	payment, _ := NewPayment(UUID{1}, amount, PaymentInitial, "Basic", now)
	require.Error(t, payment.MarkSuccess("", now), "transaction id is required")

	assert.True(t, PaymentKind("initial").Valid())
	assert.False(t, PaymentKind("bogus").Valid())
}

func TestPaymentMethod(t *testing.T) {
	now := time.Now().UTC()
	method, err := NewPaymentMethod(UUID{1}, "account-1", "token-1", "5160**9495", now.AddDate(1, 0, 0), now)
	require.NoError(t, err)
	assert.True(t, method.IsActive)
	assert.Equal(t, "card", method.Type)

	_, err = NewPaymentMethod(UUID{}, "account-1", "token-1", "", now, now)
	require.Error(t, err)
	_, err = NewPaymentMethod(UUID{1}, "account-1", "", "", now, now)
	require.Error(t, err)

	method.Unbind(now)
	assert.False(t, method.IsActive)
	assert.Empty(t, method.RecurringToken)
}

func TestSubscription_Extras(t *testing.T) {
	now := time.Now().UTC()
	snapshot, _ := NewTariffSnapshot(127, "UTC")
	period, _ := NewBillingPeriod(now.Add(-24*time.Hour), now.Add(29*24*time.Hour))

	_, err := NewSubscription(UUID{}, UUID{2}, UUID{3}, UUID{4}, snapshot, period, now)
	require.Error(t, err)
	_, err = NewSubscription(UUID{1}, UUID{2}, UUID{3}, UUID{4}, TariffSnapshot{WeekBitmap: 200}, period, now)
	require.Error(t, err)

	sub, err := NewSubscription(UUID{1}, UUID{2}, UUID{3}, UUID{4}, snapshot, period, now)
	require.NoError(t, err)
	require.NoError(t, sub.Activate(true, now))

	// Renew + scheduled change.
	newPeriod, _ := NewBillingPeriod(period.End, period.End.Add(30*24*time.Hour))
	require.NoError(t, sub.ScheduleDowngrade(UUID{5}, period.End, now))
	require.NoError(t, sub.ApplyScheduledChange(UUID{6}, UUID{5}, snapshot, period.End))
	assert.Nil(t, sub.Scheduled)
	assert.Equal(t, UUID{5}, sub.TariffPlanID)

	require.NoError(t, sub.Renew(newPeriod, period.End))
	assert.Equal(t, newPeriod.Start, sub.Period.Start)

	// Payments.
	amount, _ := NewPaymentAmount(1000, CurrencyBYN)
	payment, _ := NewPayment(UUID{7}, amount, PaymentInitial, "Basic", now)
	require.NoError(t, sub.AddPayment(payment))
	require.NoError(t, sub.AddPayment(payment), "duplicate is idempotent")
	found, ok := sub.FindPayment(UUID{7})
	require.True(t, ok)
	assert.Equal(t, payment.ID, found.ID)
	_, ok = sub.FindPayment(UUID{8})
	assert.False(t, ok)
	require.Error(t, sub.AddPayment(Payment{}))

	// Payment method replace unbinds the previous one.
	first, _ := NewPaymentMethod(UUID{10}, "a", "t1", "mask1", now, now)
	second, _ := NewPaymentMethod(UUID{11}, "a", "t2", "mask2", now, now)
	require.NoError(t, sub.SetPaymentMethod(first, now))
	require.NoError(t, sub.SetPaymentMethod(second, now))
	assert.Equal(t, second.ID, sub.PaymentMethod.ID)
	sub.RegisterPaymentFailure(&period.End, now)
	assert.Equal(t, 1, sub.RetryCount)
	sub.RegisterPaymentFailure(nil, now)
	assert.Equal(t, 2, sub.RetryCount)

	require.NoError(t, sub.Validate())
}

func TestSubscription_InvalidExtras(t *testing.T) {
	now := time.Now().UTC()
	snapshot, _ := NewTariffSnapshot(127, "UTC")
	period, _ := NewBillingPeriod(now, now.Add(30*24*time.Hour))

	sub, _ := NewSubscription(UUID{1}, UUID{2}, UUID{3}, UUID{4}, snapshot, period, now)
	require.Error(t, sub.ApplyScheduledChange(UUID{5}, UUID{4}, snapshot, now), "no scheduled change")
	require.Error(t, sub.Renew(period, now), "renew only active")

	require.NoError(t, sub.Activate(true, now))
	require.Error(t, sub.ScheduleDowngrade(UUID{}, period.End, now))
	require.Error(t, sub.ScheduleDowngrade(UUID{5}, now.Add(-time.Hour), now), "effective must be future")

	sub.Status = SubscriptionStatus("bogus")
	require.Error(t, sub.Validate())

	sub.Status = StatusActive
	sub.Period = BillingPeriod{}
	require.Error(t, sub.Validate())
}

func TestSubscriptionStatus_Valid(t *testing.T) {
	for _, status := range []SubscriptionStatus{StatusPending, StatusActive, StatusCancelled, StatusExpired, StatusSuspended} {
		assert.True(t, status.Valid())
	}
	assert.False(t, SubscriptionStatus("unknown").Valid())
}

func TestSubscriptionOffer_ValidateAndActiveAt(t *testing.T) {
	price, _ := NewPrice(1000, CurrencyBYN)
	planID := UUID{1}
	offer := SubscriptionOffer{
		ID:           UUID{2},
		TariffPlanID: planID,
		Price:        price,
		BillingDays:  30,
		ValidFrom:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		IsActive:     true,
	}
	require.NoError(t, offer.Validate())
	assert.True(t, offer.ActiveAt(offer.ValidFrom.Add(time.Hour)))
	assert.False(t, offer.ActiveAt(offer.ValidFrom.Add(-time.Hour)))
	inactive := offer
	inactive.IsActive = false
	assert.False(t, inactive.ActiveAt(offer.ValidFrom.Add(time.Hour)))

	bad := offer
	bad.BillingDays = 0
	require.Error(t, bad.Validate())
	bad = offer
	bad.ID = UUID{}
	require.Error(t, bad.Validate())
	bad = offer
	bad.ValidFrom = time.Time{}
	require.Error(t, bad.Validate())
	bad = offer
	until := offer.ValidFrom.Add(-time.Hour)
	bad.ValidUntil = &until
	require.Error(t, bad.Validate())
}

func TestBillingPeriod_Invalid(t *testing.T) {
	now := time.Now().UTC()
	_, err := NewBillingPeriod(time.Time{}, now)
	require.Error(t, err)
	_, err = NewBillingPeriod(now, now.Add(-time.Hour))
	require.Error(t, err)
}

func TestParsePaymentAmount(t *testing.T) {
	money, err := ParsePaymentAmount("1", "BYN")
	require.NoError(t, err)
	assert.Equal(t, int64(1), money.AmountKopecks)

	_, err = ParsePaymentAmount("1", "GBP")
	require.Error(t, err)
}

func TestMoney_AddSuccess(t *testing.T) {
	first, _ := NewPaymentAmount(100, CurrencyBYN)
	second, _ := NewPaymentAmount(200, CurrencyBYN)
	sum, err := first.Add(second)
	require.NoError(t, err)
	assert.Equal(t, int64(300), sum.AmountKopecks)
}
