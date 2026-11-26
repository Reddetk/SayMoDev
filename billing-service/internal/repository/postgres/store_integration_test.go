//go:build integration

package postgres

// Интеграционные проверки схемы и адаптера на реальном PostgreSQL.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Reddetk/SayMoDev/billing-service/internal/port/out"
)

func TestStore_Integration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://saymo:changeme@localhost:5442/billing_db?sslmode=disable"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, pool.Ping(ctx))

	store := New(pool)
	now := time.Now().UTC()
	accountID := uuid.NewString()
	sub := out.SubscriptionRecord{
		ID: uuid.NewString(), AccountID: accountID,
		TariffPlanID: "11111111-1111-1111-1111-111111111111",
		OfferID:      "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa1",
		Status:       "active", WeekBitmap: 127, Timezone: "UTC",
		PeriodStart: now, PeriodEnd: now.AddDate(0, 0, 30), CreatedAt: now, UpdatedAt: now,
	}

	require.NoError(t, store.CreateSubscription(ctx, sub))
	t.Cleanup(func() { _ = store.DeleteSubscription(context.Background(), sub.ID) })

	// §1: partial unique index — вторая живая подписка запрещена.
	duplicate := sub
	duplicate.ID = uuid.NewString()
	require.Error(t, store.CreateSubscription(ctx, duplicate))

	loaded, err := store.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, sub.AccountID, loaded.AccountID)

	// FOR UPDATE внутри транзакции.
	require.NoError(t, store.WithinTx(ctx, func(tx out.Store) error {
		locked, err := tx.GetSubscriptionForUpdate(ctx, sub.ID)
		require.NoError(t, err)
		assert.Equal(t, sub.ID, locked.ID)
		return nil
	}))

	// Только одна живая подписка возвращается по аккаунту.
	byAccount, err := store.GetSubscriptionByAccount(ctx, accountID)
	require.NoError(t, err)
	assert.Equal(t, sub.ID, byAccount.ID)

	// SQL-семантика due-выборки: retry-гейт и pending renewal.
	sub.Status = "active"
	sub.PeriodStart = now.Add(-2 * time.Hour)
	sub.PeriodEnd = now.Add(-time.Hour)
	future := now.Add(time.Hour)
	sub.NextRetryAt = &future
	require.NoError(t, store.UpdateSubscription(ctx, sub))
	due, err := store.ListSubscriptionsDue(ctx, "active", now, 100)
	require.NoError(t, err)
	assert.False(t, containsSubscription(due, sub.ID), "future next_retry_at blocks retry")

	sub.NextRetryAt = nil
	require.NoError(t, store.UpdateSubscription(ctx, sub))
	due, err = store.ListSubscriptionsDue(ctx, "active", now, 100)
	require.NoError(t, err)
	assert.True(t, containsSubscription(due, sub.ID))

	renewalID := uuid.NewString()
	require.NoError(t, store.CreatePayment(ctx, out.PaymentRecord{
		ID: renewalID, SubscriptionID: sub.ID, AmountKopecks: 2990, Currency: "BYN",
		Status: "pending", Kind: "renewal", TariffPlanName: "Basic", CreatedAt: now, UpdatedAt: now,
	}))
	due, err = store.ListSubscriptionsDue(ctx, "active", now, 100)
	require.NoError(t, err)
	assert.False(t, containsSubscription(due, sub.ID), "pending renewal blocks duplicate")

	require.NoError(t, store.UpdatePayment(ctx, out.PaymentRecord{
		ID: renewalID, SubscriptionID: sub.ID, AmountKopecks: 2990, Currency: "BYN",
		Status: "failed", Kind: "renewal", TariffPlanName: "Basic", CreatedAt: now, UpdatedAt: now,
	}))
	due, err = store.ListSubscriptionsDue(ctx, "active", now, 100)
	require.NoError(t, err)
	assert.True(t, containsSubscription(due, sub.ID), "failed renewal allows retry")

	// Идемпотентность: первый INSERT true, повторный false.
	key := uuid.NewString()
	created, err := store.CreateIdempotency(ctx, out.IdempotencyRecord{Key: key, AccountID: accountID, Endpoint: "/billing/subscriptions"})
	require.NoError(t, err)
	assert.True(t, created)
	created, err = store.CreateIdempotency(ctx, out.IdempotencyRecord{Key: key, AccountID: accountID, Endpoint: "/billing/subscriptions"})
	require.NoError(t, err)
	assert.False(t, created)
	require.NoError(t, store.CompleteIdempotency(ctx, out.IdempotencyRecord{
		Key: key, AccountID: accountID, ResponseCode: 201, ResponseBody: []byte(`{"ok":true}`),
	}))
	record, err := store.GetIdempotency(ctx, accountID, key)
	require.NoError(t, err)
	assert.Equal(t, 201, record.ResponseCode)

	// Outbox: insert → fetch → mark sent.
	eventID := uuid.NewString()
	require.NoError(t, store.InsertOutbox(ctx, eventID, "SubscriptionActivated", []byte(`{"payload":{}}`)))
	records, err := store.FetchOutboxBatch(ctx, 1000)
	require.NoError(t, err)
	found := false
	for _, rec := range records {
		if rec.EventID == eventID {
			found = true
			require.NoError(t, store.MarkOutboxSent(ctx, rec.ID))
		}
	}
	assert.True(t, found)

	// account_status projection.
	require.NoError(t, store.UpsertAccountStatus(ctx, accountID, "locked"))
	status, err := store.GetAccountStatus(ctx, accountID)
	require.NoError(t, err)
	assert.Equal(t, "locked", status)

	// payments: refund с отрицательной суммой проходит CHECK.
	paymentID := uuid.NewString()
	require.NoError(t, store.CreatePayment(ctx, out.PaymentRecord{
		ID: paymentID, SubscriptionID: sub.ID, AmountKopecks: -2990, Currency: "BYN",
		Status: "success", Kind: "initial", TariffPlanName: "Basic (refund)",
		CreatedAt: now, UpdatedAt: now,
	}))
	payment, err := store.GetPayment(ctx, paymentID)
	require.NoError(t, err)
	assert.EqualValues(t, -2990, payment.AmountKopecks)

	// Метаданные компенсации upgrade_proration + выборка stale.
	prorationID := uuid.NewString()
	applied := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa3"
	revertPlan := "11111111-1111-1111-1111-111111111111"
	revertOffer := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa1"
	bitmap := int16(127)
	tz := "UTC"
	require.NoError(t, store.CreatePayment(ctx, out.PaymentRecord{
		ID: prorationID, SubscriptionID: sub.ID, AmountKopecks: 6990, Currency: "BYN",
		Status: "pending", Kind: "upgrade_proration", TariffPlanName: "Premium",
		AppliedOfferID: &applied, RevertTariffPlanID: &revertPlan, RevertOfferID: &revertOffer,
		RevertWeekBitmap: &bitmap, RevertTimezone: &tz,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}))
	stale, err := store.ListStaleProrationPayments(ctx, now.Add(-30*time.Minute), 10)
	require.NoError(t, err)
	foundProration := false
	for _, rec := range stale {
		if rec.ID == prorationID {
			foundProration = true
			require.NotNil(t, rec.AppliedOfferID)
			require.NotNil(t, rec.RevertOfferID)
		}
	}
	assert.True(t, foundProration)

	// now() из БД (§11).
	dbNow, err := store.Now(ctx)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), dbNow, 5*time.Second)
}

func containsSubscription(records []out.SubscriptionRecord, id string) bool {
	for _, rec := range records {
		if rec.ID == id {
			return true
		}
	}
	return false
}
