package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Reddetk/SayMoDev/billing-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/billing-service/internal/port/out"
)

var (
	errDuplicateKey = errors.New("duplicate key value violates unique constraint")
	errAcquiring    = errors.New("acquiring down")
)

const (
	testBasicPlan     = "11111111-1111-1111-1111-111111111111"
	testStandardPlan  = "22222222-2222-2222-2222-222222222222"
	testPremiumPlan   = "33333333-3333-3333-3333-333333333333"
	testBasicOffer    = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa1"
	testStandardOffer = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa2"
	testPremiumOffer  = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa3"
)

func newTestService(t *testing.T) (*Service, *fakeStore, *fakeCache, *fakeAcquiring, *fakeNotifier) {
	t.Helper()
	store := newFakeStore()
	now := time.Now().UTC()
	addOffer := func(offerID, planID, name string, price int64, bitmap int16) {
		store.offers[offerID] = out.OfferRecord{
			OfferID: offerID, TariffPlanID: planID, PlanName: name, WeekBitmap: bitmap, Timezone: "UTC",
			PriceKopecks: price, Currency: "BYN", BillingDays: 30, ValidFrom: now.Add(-time.Hour), IsActive: true,
		}
	}
	addOffer(testBasicOffer, testBasicPlan, "Basic", 2990, 127)
	addOffer(testStandardOffer, testStandardPlan, "Standard", 4990, 31)
	addOffer(testPremiumOffer, testPremiumPlan, "Premium", 9990, 127)

	cache := newFakeCache()
	acquiring := &fakeAcquiring{}
	notifier := &fakeNotifier{}
	service := New(store, cache, acquiring, notifier, Config{
		StoreID: "store", NotifyURL: "http://notify", ReturnURL: "http://return", WebhookSecret: "secret",
	})
	return service, store, cache, acquiring, notifier
}

func subscribeAndActivate(t *testing.T, service *Service, store *fakeStore, accountID, planID string) string {
	t.Helper()
	result, err := service.Subscribe(context.Background(), in.AuthContext{AccountID: accountID, Role: "patient"}, in.SubscribeInput{
		AccountID: accountID, TariffPlanID: planID,
	})
	require.NoError(t, err)
	require.Equal(t, "pending", result.Status)

	payment, err := store.GetPayment(context.Background(), result.Payment.PaymentID)
	require.NoError(t, err)
	input := successWebhook(t, *payment, "tx-"+uuid.NewString())
	require.NoError(t, service.HandleWebhook(context.Background(), input))
	return result.SubscriptionID
}

func successWebhook(t *testing.T, payment out.PaymentRecord, transactionID string) in.WebhookInput {
	t.Helper()
	input := in.WebhookInput{
		BatchTimestamp: time.Now().Unix(),
		CurrencyID:     payment.Currency,
		Amount:         fmt.Sprintf("%d.%02d", payment.AmountKopecks/100, payment.AmountKopecks%100),
		PaymentMethod:  "cc",
		OrderID:        "order",
		SiteOrderID:    payment.ID,
		TransactionID:  transactionID,
		PaymentType:    1,
		RRN:            "123",
		Card:           "5160**9495",
		Action:         0,
		RC:             "W0001",
		CustomerID:     "customer",
		OperationType:  "recurring_bind",
		RecurringToken: "token-" + transactionID,
		OfferExpDate:   time.Now().AddDate(1, 0, 0).Format("2006-01-02"),
	}
	input.Signature = webhookSignature(input, "secret")
	return input
}

func TestSubscribe_SuccessAndDuplicate(t *testing.T) {
	service, store, _, acquiring, _ := newTestService(t)
	accountID := uuid.NewString()

	result, err := service.Subscribe(context.Background(), in.AuthContext{AccountID: accountID}, in.SubscribeInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
	})
	require.NoError(t, err)
	assert.Equal(t, "pending", result.Status)
	assert.EqualValues(t, 2990, result.Payment.AmountKopecks)
	assert.Contains(t, result.Payment.AcquiringRedirectURL, "https://pay.example")
	assert.Equal(t, 1, acquiring.linkCalls)

	_, err = service.Subscribe(context.Background(), in.AuthContext{AccountID: accountID}, in.SubscribeInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
	})
	var appErr *Error
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, CodeActiveSubscription, appErr.Code)

	records, _ := store.ListPayments(context.Background(), result.SubscriptionID)
	assert.Len(t, records, 1)
}

func TestActivation_WebhookSuccess(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)

	rec, err := store.GetSubscription(context.Background(), subscriptionID)
	require.NoError(t, err)
	assert.Equal(t, "active", rec.Status)

	method, err := store.GetActivePaymentMethod(context.Background(), subscriptionID)
	require.NoError(t, err)
	assert.NotEmpty(t, method.RecurringToken)

	events := store.eventTypes()
	assert.Contains(t, events, "SubscriptionActivated")
	assert.Contains(t, events, "TariffPlanActivated")
	assert.Contains(t, events, "PaymentSucceeded")
}

func TestWebhook_InvalidSignatureAndAmount(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	result, err := service.Subscribe(context.Background(), in.AuthContext{AccountID: accountID}, in.SubscribeInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
	})
	require.NoError(t, err)
	payment, _ := store.GetPayment(context.Background(), result.Payment.PaymentID)

	input := successWebhook(t, *payment, "tx-bad")
	input.Signature = "deadbeef"
	require.Error(t, service.HandleWebhook(context.Background(), input))

	input = successWebhook(t, *payment, "tx-bad2")
	input.Amount = "999.00"
	require.Error(t, service.HandleWebhook(context.Background(), input))
}

func TestWebhook_FailureKeepsPending(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	result, err := service.Subscribe(context.Background(), in.AuthContext{AccountID: accountID}, in.SubscribeInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
	})
	require.NoError(t, err)
	payment, _ := store.GetPayment(context.Background(), result.Payment.PaymentID)

	input := successWebhook(t, *payment, "tx-fail")
	input.Action = 1
	input.PaymentType = 2
	input.Signature = webhookSignature(input, "secret")
	require.NoError(t, service.HandleWebhook(context.Background(), input))

	rec, _ := store.GetSubscription(context.Background(), result.SubscriptionID)
	assert.Equal(t, "pending", rec.Status)
	payment, _ = store.GetPayment(context.Background(), result.Payment.PaymentID)
	assert.Equal(t, "failed", payment.Status)
	assert.Contains(t, store.eventTypes(), "PaymentFailed")
}

func TestPreviewAndUpgrade(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	auth := in.AuthContext{AccountID: accountID}

	preview, err := service.PreviewUpgrade(context.Background(), auth, subscriptionID, testPremiumPlan)
	require.NoError(t, err)
	assert.NotEmpty(t, preview.QuoteID)
	assert.Greater(t, preview.Proration.AmountKopecks, int64(0))

	result, err := service.Upgrade(context.Background(), auth, in.UpgradeInput{
		SubscriptionID: subscriptionID, TargetTariff: testPremiumPlan, QuoteID: preview.QuoteID,
	})
	require.NoError(t, err)
	assert.Equal(t, "active", result.Status)
	assert.Greater(t, result.ProrationCharge.AmountKopecks, int64(0))

	rec, _ := store.GetSubscription(context.Background(), subscriptionID)
	assert.Equal(t, testPremiumPlan, rec.TariffPlanID)
	assert.Contains(t, store.eventTypes(), "SubscriptionChanged")
}

func TestUpgrade_QuoteExpiredAndRateLimit(t *testing.T) {
	service, store, cache, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	auth := in.AuthContext{AccountID: accountID}

	_, err := service.Upgrade(context.Background(), auth, in.UpgradeInput{
		SubscriptionID: subscriptionID, TargetTariff: testPremiumPlan, QuoteID: uuid.NewString(),
	})
	var quoteErr *in.QuoteExpiredError
	require.True(t, errors.As(err, &quoteErr))
	assert.NotEmpty(t, quoteErr.Refreshed.QuoteID)

	cache.allow = false
	_, err = service.Upgrade(context.Background(), auth, in.UpgradeInput{
		SubscriptionID: subscriptionID, TargetTariff: testPremiumPlan,
	})
	var appErr *Error
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, CodeRateLimit, appErr.Code)
}

func TestDowngrade(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testStandardPlan)

	result, err := service.Downgrade(context.Background(), in.AuthContext{AccountID: accountID}, in.DowngradeInput{
		SubscriptionID: subscriptionID, TargetTariff: testBasicPlan,
	})
	require.NoError(t, err)
	require.NotNil(t, result.ScheduledChange)
	assert.Equal(t, testBasicPlan, result.ScheduledChange.TargetTariffPlanID)
}

func TestCancelAndResumeGuards(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	auth := in.AuthContext{AccountID: accountID}

	cancelled, err := service.Cancel(context.Background(), auth, subscriptionID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", cancelled.Status)

	_, err = service.Resume(context.Background(), auth, subscriptionID)
	var appErr *Error
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, CodeNotSuspended, appErr.Code)
}

func TestOwnership_HidesForeignSubscription(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	owner := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, owner, testBasicPlan)

	_, err := service.GetSubscription(context.Background(), in.AuthContext{AccountID: uuid.NewString()}, subscriptionID)
	var appErr *Error
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, CodeNotFound, appErr.Code)

	view, err := service.GetSubscription(context.Background(), in.AuthContext{AccountID: owner}, subscriptionID)
	require.NoError(t, err)
	assert.Equal(t, "active", view.Status)

	payments, err := service.ListPayments(context.Background(), in.AuthContext{AccountID: owner}, subscriptionID)
	require.NoError(t, err)
	assert.NotEmpty(t, payments)
}

func TestIdentityEvents_LockUnlockDelete(t *testing.T) {
	service, store, _, acquiring, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	ctx := context.Background()

	lock := IdentityEvent{EventID: uuid.NewString(), EventType: "AccountLockedByAdmin",
		Payload: json.RawMessage(`{"accountId":"` + accountID + `","reason":"admin"}`)}
	require.NoError(t, service.HandleIdentityEvent(ctx, lock))
	rec, _ := store.GetSubscription(ctx, subscriptionID)
	assert.Equal(t, "suspended", rec.Status)

	require.NoError(t, service.HandleIdentityEvent(ctx, lock), "duplicate event is idempotent")

	unlock := IdentityEvent{EventID: uuid.NewString(), EventType: "AccountUnlocked",
		Payload: json.RawMessage(`{"accountId":"` + accountID + `"}`)}
	require.NoError(t, service.HandleIdentityEvent(ctx, unlock))
	rec, _ = store.GetSubscription(ctx, subscriptionID)
	assert.Equal(t, "active", rec.Status)

	del := IdentityEvent{EventID: uuid.NewString(), EventType: "AccountDeleted",
		Payload: json.RawMessage(`{"accountId":"` + accountID + `"}`)}
	require.NoError(t, service.HandleIdentityEvent(ctx, del))
	_, err := store.GetSubscription(ctx, subscriptionID)
	require.ErrorIs(t, err, out.ErrNotFound)
	assert.Equal(t, 1, acquiring.unbindCalls)
	assert.Len(t, store.archives, 1)
}

func TestLifecycle_RenewalFailureSchedulesRetry(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	now := time.Now().UTC()
	_, err := service.AdminActivateTariff(context.Background(), in.AuthContext{Role: "administrator"}, in.AdminActivateInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
		PeriodStart: ptrTime(now.AddDate(0, 0, -31)), PeriodEnd: ptrTime(now.AddDate(0, 0, -1)),
	})
	require.NoError(t, err)

	require.NoError(t, service.LifecycleTick(context.Background()))

	rec, err := store.GetSubscriptionByAccount(context.Background(), accountID)
	require.NoError(t, err)
	assert.Equal(t, 1, rec.RetryCount)
	require.NotNil(t, rec.NextRetryAt)
	assert.Contains(t, store.eventTypes(), "PaymentFailed")
}

func TestResumeSuspendedAndAdminDeactivate(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	ctx := context.Background()

	lock := IdentityEvent{EventID: uuid.NewString(), EventType: "AccountLockedByFailedAttempts",
		Payload: json.RawMessage(`{"accountId":"` + accountID + `","reason":"bruteforce"}`)}
	require.NoError(t, service.HandleIdentityEvent(ctx, lock))

	resumed, err := service.Resume(ctx, in.AuthContext{AccountID: accountID}, subscriptionID)
	require.NoError(t, err)
	assert.Equal(t, "active", resumed.Status)

	deactivated, err := service.AdminDeactivateTariff(ctx, in.AuthContext{Role: "administrator"}, accountID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", deactivated.Status)

	_, err = service.PatchSubscription(ctx, in.AuthContext{Role: "patient"}, in.PatchInput{SubscriptionID: subscriptionID})
	var appErr *Error
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, CodeForbidden, appErr.Code)
}

func TestPatchSubscription_AdminSetsSchedule(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testPremiumPlan)
	effective := time.Now().UTC().AddDate(0, 0, 30)

	view, err := service.PatchSubscription(context.Background(), in.AuthContext{Role: "administrator"}, in.PatchInput{
		SubscriptionID: subscriptionID, TargetTariffPlanID: testBasicPlan, EffectiveAt: &effective,
	})
	require.NoError(t, err)
	require.NotNil(t, view.ScheduledChange)
	assert.Equal(t, testBasicPlan, view.ScheduledChange.TargetTariffPlanID)
}

func TestLifecycle_SuspendedExpiry(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	now := time.Now().UTC()
	_, err := service.AdminActivateTariff(context.Background(), in.AuthContext{Role: "administrator"}, in.AdminActivateInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
		PeriodStart: ptrTime(now.AddDate(0, 0, -31)), PeriodEnd: ptrTime(now.AddDate(0, 0, -1)),
	})
	require.NoError(t, err)

	lock := IdentityEvent{EventID: uuid.NewString(), EventType: "AccountLockedByAdmin",
		Payload: json.RawMessage(`{"accountId":"` + accountID + `"}`)}
	require.NoError(t, service.HandleIdentityEvent(context.Background(), lock))

	require.NoError(t, service.LifecycleTick(context.Background()))

	rec, err := store.GetSubscriptionByAccount(context.Background(), accountID)
	if err == nil {
		assert.Equal(t, "expired", rec.Status)
	}
	events := store.eventTypes()
	assert.Contains(t, events, "SubscriptionExpired")
	assert.Contains(t, events, "TariffPlanDeActivated")
}

func TestWebhook_RenewalSuccess(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	ctx := context.Background()

	// Сдвигаем период в прошлое, чтобы worker инициировал renewal.
	rec, _ := store.GetSubscription(ctx, subscriptionID)
	rec.PeriodStart = time.Now().UTC().AddDate(0, 0, -31)
	rec.PeriodEnd = time.Now().UTC().AddDate(0, 0, -1)
	require.NoError(t, store.UpdateSubscription(ctx, *rec))

	require.NoError(t, service.LifecycleTick(ctx))

	var renewal *out.PaymentRecord
	payments, _ := store.ListPayments(ctx, subscriptionID)
	for i := range payments {
		if payments[i].Kind == "renewal" {
			renewal = &payments[i]
		}
	}
	require.NotNil(t, renewal)
	assert.Equal(t, "pending", renewal.Status)

	input := successWebhook(t, *renewal, "tx-renew")
	input.PaymentType = 10
	input.OperationType = "recurring_pay"
	input.Signature = webhookSignature(input, "secret")
	require.NoError(t, service.HandleWebhook(ctx, input))

	assert.Contains(t, store.eventTypes(), "SubscriptionRenewed")
}

func TestWebhook_LateSuccessAfterFailureIgnored(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	result, err := service.Subscribe(context.Background(), in.AuthContext{AccountID: accountID}, in.SubscribeInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
	})
	require.NoError(t, err)
	payment, _ := store.GetPayment(context.Background(), result.Payment.PaymentID)

	failure := successWebhook(t, *payment, "tx-1")
	failure.Action = 1
	failure.PaymentType = 2
	failure.Signature = webhookSignature(failure, "secret")
	require.NoError(t, service.HandleWebhook(context.Background(), failure))

	lateSuccess := successWebhook(t, *payment, "tx-2")
	lateSuccess.Signature = webhookSignature(lateSuccess, "secret")
	require.NoError(t, service.HandleWebhook(context.Background(), lateSuccess))

	payment, _ = store.GetPayment(context.Background(), result.Payment.PaymentID)
	assert.Equal(t, "failed", payment.Status, "failed is final")
	rec, _ := store.GetSubscription(context.Background(), result.SubscriptionID)
	assert.Equal(t, "pending", rec.Status)
}

func TestLifecycle_RenewalDoesNotExtendPeriodBeforePayment(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	now := time.Now().UTC()
	_, err := service.AdminActivateTariff(context.Background(), in.AuthContext{Role: "administrator"}, in.AdminActivateInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
		PeriodStart: ptrTime(now.AddDate(0, 0, -31)), PeriodEnd: ptrTime(now.AddDate(0, 0, -1)),
	})
	require.NoError(t, err)

	before, _ := store.GetSubscriptionByAccount(context.Background(), accountID)
	require.NoError(t, service.LifecycleTick(context.Background()))
	after, _ := store.GetSubscriptionByAccount(context.Background(), accountID)
	assert.Equal(t, before.PeriodEnd, after.PeriodEnd, "period moves only after successful webhook")
}

func TestLifecycle_ThreeFailuresExpire(t *testing.T) {
	service, store, _, _, notifier := newTestService(t)
	accountID := uuid.NewString()
	now := time.Now().UTC()
	_, err := service.AdminActivateTariff(context.Background(), in.AuthContext{Role: "administrator"}, in.AdminActivateInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
		PeriodStart: ptrTime(now.AddDate(0, 0, -31)), PeriodEnd: ptrTime(now.AddDate(0, 0, -1)),
	})
	require.NoError(t, err)

	for attempt := 1; attempt <= 3; attempt++ {
		require.NoError(t, service.LifecycleTick(context.Background()))
		rec, err := store.GetSubscriptionByAccount(context.Background(), accountID)
		if err != nil {
			break
		}
		if attempt < 3 {
			assert.Equal(t, attempt, rec.RetryCount)
			assert.NotNil(t, rec.NextRetryAt)
			// ускоряем следующую попытку
			rec.NextRetryAt = ptrTime(time.Now().UTC().Add(-time.Second))
			require.NoError(t, store.UpdateSubscription(context.Background(), *rec))
		}
	}

	rec, err := store.GetSubscriptionByAccount(context.Background(), accountID)
	require.ErrorIs(t, err, out.ErrNotFound, "expired is not alive")
	if rec != nil {
		assert.Equal(t, "expired", rec.Status)
	}
	events := store.eventTypes()
	assert.Contains(t, events, "SubscriptionExpired")
	assert.Contains(t, events, "TariffPlanDeActivated")
	assert.NotEmpty(t, notifier.sent)
}

func TestLifecycle_RetryGateHonorsNextRetry(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	now := time.Now().UTC()
	_, err := service.AdminActivateTariff(context.Background(), in.AuthContext{Role: "administrator"}, in.AdminActivateInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
		PeriodStart: ptrTime(now.AddDate(0, 0, -31)), PeriodEnd: ptrTime(now.AddDate(0, 0, -1)),
	})
	require.NoError(t, err)

	require.NoError(t, service.LifecycleTick(context.Background()))
	rec, _ := store.GetSubscriptionByAccount(context.Background(), accountID)
	require.Equal(t, 1, rec.RetryCount)
	require.NotNil(t, rec.NextRetryAt)

	payments := countRenewals(t, store, rec.ID)
	// next_retry_at в будущем — новый платёж не создаётся.
	require.NoError(t, service.LifecycleTick(context.Background()))
	rec, _ = store.GetSubscriptionByAccount(context.Background(), accountID)
	assert.Equal(t, 1, rec.RetryCount)
	assert.Equal(t, payments, countRenewals(t, store, rec.ID))
}

func TestLifecycle_StaleRenewalPaymentReconciled(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	now := time.Now().UTC()
	_, err := service.AdminActivateTariff(context.Background(), in.AuthContext{Role: "administrator"}, in.AdminActivateInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
		PeriodStart: ptrTime(now.AddDate(0, 0, -31)), PeriodEnd: ptrTime(now.AddDate(0, 0, -1)),
	})
	require.NoError(t, err)
	rec, _ := store.GetSubscriptionByAccount(context.Background(), accountID)

	// Платёж, на который webhook так и не пришёл.
	stale := out.PaymentRecord{
		ID: uuid.NewString(), SubscriptionID: rec.ID, AmountKopecks: 2990, Currency: "BYN",
		Status: "pending", Kind: "renewal", TariffPlanName: "Basic", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	require.NoError(t, store.CreatePayment(context.Background(), stale))

	require.NoError(t, service.LifecycleTick(context.Background()))
	payment, err := store.GetPayment(context.Background(), stale.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", payment.Status)
	rec, _ = store.GetSubscriptionByAccount(context.Background(), accountID)
	assert.Equal(t, 1, rec.RetryCount)
}

func TestWebhook_RefundCreatesNegativePayment(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	result, err := service.Subscribe(context.Background(), in.AuthContext{AccountID: accountID}, in.SubscribeInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
	})
	require.NoError(t, err)
	payment, _ := store.GetPayment(context.Background(), result.Payment.PaymentID)

	success := successWebhook(t, *payment, "tx-ok")
	require.NoError(t, service.HandleWebhook(context.Background(), success))

	refund := successWebhook(t, *payment, "tx-refund")
	refund.PaymentType = 5
	refund.Signature = webhookSignature(refund, "secret")
	require.NoError(t, service.HandleWebhook(context.Background(), refund))

	payments, _ := store.ListPayments(context.Background(), result.SubscriptionID)
	var negative int
	for _, item := range payments {
		if item.AmountKopecks < 0 {
			negative++
			assert.EqualValues(t, -2990, item.AmountKopecks)
		}
	}
	assert.Equal(t, 1, negative)
	original, _ := store.GetPayment(context.Background(), payment.ID)
	assert.Equal(t, "success", original.Status, "original success is immutable")
}

func TestLifecycle_CancelledExpiry(t *testing.T) {
	service, store, _, acquiring, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	ctx := context.Background()

	_, err := service.Cancel(ctx, in.AuthContext{AccountID: accountID}, subscriptionID)
	require.NoError(t, err)
	rec, _ := store.GetSubscription(ctx, subscriptionID)
	rec.PeriodStart = time.Now().UTC().AddDate(0, 0, -31)
	rec.PeriodEnd = time.Now().UTC().Add(-time.Minute)
	require.NoError(t, store.UpdateSubscription(ctx, *rec))

	require.NoError(t, service.LifecycleTick(ctx))
	_, err = store.GetSubscriptionByAccount(ctx, accountID)
	require.ErrorIs(t, err, out.ErrNotFound)
	assert.Contains(t, store.eventTypes(), "TariffPlanDeActivated")
	assert.GreaterOrEqual(t, acquiring.unbindCalls, 1)
}

func TestLifecycle_StalePendingAbandoned(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	result, err := service.Subscribe(context.Background(), in.AuthContext{AccountID: accountID}, in.SubscribeInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
	})
	require.NoError(t, err)
	rec, _ := store.GetSubscription(context.Background(), result.SubscriptionID)
	rec.CreatedAt = time.Now().UTC().Add(-25 * time.Hour)
	require.NoError(t, store.UpdateSubscription(context.Background(), *rec))

	require.NoError(t, service.LifecycleTick(context.Background()))
	_, err = store.GetSubscriptionByAccount(context.Background(), accountID)
	require.ErrorIs(t, err, out.ErrNotFound)
	assert.Contains(t, store.eventTypes(), "SubscriptionExpired")
}

func TestUpgrade_ChargeFailureRevertsTariff(t *testing.T) {
	service, store, _, acquiring, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	auth := in.AuthContext{AccountID: accountID}

	preview, err := service.PreviewUpgrade(context.Background(), auth, subscriptionID, testPremiumPlan)
	require.NoError(t, err)

	acquiring.failRecurring = true
	_, err = service.Upgrade(context.Background(), auth, in.UpgradeInput{
		SubscriptionID: subscriptionID, TargetTariff: testPremiumPlan, QuoteID: preview.QuoteID,
	})
	var appErr *Error
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, CodePaymentFailed, appErr.Code)

	rec, _ := store.GetSubscription(context.Background(), subscriptionID)
	assert.Equal(t, testBasicPlan, rec.TariffPlanID, "tariff rolled back")
}

func countRenewals(t *testing.T, store *fakeStore, subscriptionID string) int {
	t.Helper()
	payments, err := store.ListPayments(context.Background(), subscriptionID)
	require.NoError(t, err)
	count := 0
	for _, payment := range payments {
		if payment.Kind == "renewal" {
			count++
		}
	}
	return count
}

func TestLifecycle_StaleProrationRevertsTariff(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	auth := in.AuthContext{AccountID: accountID}

	preview, err := service.PreviewUpgrade(context.Background(), auth, subscriptionID, testPremiumPlan)
	require.NoError(t, err)
	_, err = service.Upgrade(context.Background(), auth, in.UpgradeInput{
		SubscriptionID: subscriptionID, TargetTariff: testPremiumPlan, QuoteID: preview.QuoteID,
	})
	require.NoError(t, err)

	// Webhook не пришёл: платёж висит pending дольше 30 минут.
	payments, err := store.ListPayments(context.Background(), subscriptionID)
	require.NoError(t, err)
	var proration out.PaymentRecord
	for _, payment := range payments {
		if payment.Kind == "upgrade_proration" {
			proration = payment
		}
	}
	require.NotEmpty(t, proration.ID)
	proration.CreatedAt = time.Now().UTC().Add(-time.Hour)
	require.NoError(t, store.UpdatePayment(context.Background(), proration))

	require.NoError(t, service.LifecycleTick(context.Background()))

	rec, err := store.GetSubscription(context.Background(), subscriptionID)
	require.NoError(t, err)
	assert.Equal(t, testBasicPlan, rec.TariffPlanID, "tariff rolled back")
	payment, err := store.GetPayment(context.Background(), proration.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", payment.Status)
	assert.Contains(t, store.eventTypes(), "PaymentFailed")
	assert.Contains(t, store.eventTypes(), "SubscriptionChanged")
	assert.Contains(t, store.eventTypes(), "TariffPlanChanged")
}

func TestUpgrade_QuoteMismatchReturns409(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	auth := in.AuthContext{AccountID: accountID}

	preview, err := service.PreviewUpgrade(context.Background(), auth, subscriptionID, testPremiumPlan)
	require.NoError(t, err)

	// Чужой quote (другой аккаунт) не должен приниматься.
	otherAuth := in.AuthContext{AccountID: uuid.NewString()}
	_, err = service.Upgrade(context.Background(), otherAuth, in.UpgradeInput{
		SubscriptionID: subscriptionID, TargetTariff: testPremiumPlan, QuoteID: preview.QuoteID,
	})
	var appErr *Error
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, CodeNotFound, appErr.Code)

	// Подмена target tariff в quote → 409 quote_expired с обновлённым preview.
	_, err = service.Upgrade(context.Background(), auth, in.UpgradeInput{
		SubscriptionID: subscriptionID, TargetTariff: testStandardPlan, QuoteID: preview.QuoteID,
	})
	var quoteErr *in.QuoteExpiredError
	require.True(t, errors.As(err, &quoteErr))
	require.NotNil(t, quoteErr.Refreshed)
}

func ptrTime(t time.Time) *time.Time { return &t }
