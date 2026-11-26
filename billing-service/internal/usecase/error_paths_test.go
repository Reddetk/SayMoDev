package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Reddetk/SayMoDev/billing-service/internal/domain"
	"github.com/Reddetk/SayMoDev/billing-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/billing-service/internal/port/out"
)

type fakeMetrics struct{ counts map[string]int }

func (m *fakeMetrics) inc(key string) {
	if m.counts == nil {
		m.counts = map[string]int{}
	}
	m.counts[key]++
}
func (m *fakeMetrics) SubscriptionActivation(string)   { m.inc("activation") }
func (m *fakeMetrics) PaymentResult(string)            { m.inc("payment") }
func (m *fakeMetrics) WebhookReceived()                { m.inc("webhook") }
func (m *fakeMetrics) WebhookSignatureFailure()        { m.inc("signature") }
func (m *fakeMetrics) WebhookLateSuccess()             { m.inc("late_success") }
func (m *fakeMetrics) PaymentDuration(string, float64) { m.inc("duration") }
func (m *fakeMetrics) SubscriptionChange(string)       { m.inc("change") }

func TestSubscribe_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	auth := in.AuthContext{AccountID: uuid.NewString()}

	t.Run("empty tariff", func(t *testing.T) {
		service, _, _, _, _ := newTestService(t)
		_, err := service.Subscribe(ctx, auth, in.SubscribeInput{AccountID: auth.AccountID})
		requireAppError(t, err, CodeInvalidInput)
	})

	t.Run("no active offer", func(t *testing.T) {
		service, _, _, _, _ := newTestService(t)
		_, err := service.Subscribe(ctx, auth, in.SubscribeInput{AccountID: auth.AccountID, TariffPlanID: uuid.NewString()})
		requireAppError(t, err, CodeInvalidInput)
	})

	t.Run("store error", func(t *testing.T) {
		service, store, _, _, _ := newTestService(t)
		store.fail["CreateSubscription"] = errors.New("db down")
		_, err := service.Subscribe(ctx, auth, in.SubscribeInput{AccountID: auth.AccountID, TariffPlanID: testBasicPlan})
		require.Error(t, err)
	})

	t.Run("unique violation", func(t *testing.T) {
		service, store, _, _, _ := newTestService(t)
		store.fail["CreateSubscription"] = errors.New("duplicate key 23505")
		_, err := service.Subscribe(ctx, auth, in.SubscribeInput{AccountID: auth.AccountID, TariffPlanID: testBasicPlan})
		requireAppError(t, err, CodeActiveSubscription)
	})

	t.Run("acquiring down", func(t *testing.T) {
		service, _, _, acquiring, _ := newTestService(t)
		acquiring.linkErr = errors.New("acquiring down")
		_, err := service.Subscribe(ctx, auth, in.SubscribeInput{AccountID: auth.AccountID, TariffPlanID: testBasicPlan})
		require.Error(t, err)
	})
}

func TestGetAndList_ErrorPaths(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	ctx := context.Background()
	owner := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, owner, testBasicPlan)

	_, err := service.GetSubscription(ctx, in.AuthContext{AccountID: uuid.NewString()}, subscriptionID)
	requireAppError(t, err, CodeNotFound)

	store.fail["GetSubscription"] = errors.New("db down")
	_, err = service.GetSubscription(ctx, in.AuthContext{AccountID: owner}, subscriptionID)
	require.Error(t, err)
	delete(store.fail, "GetSubscription")

	store.fail["ListPayments"] = errors.New("db down")
	_, err = service.ListPayments(ctx, in.AuthContext{AccountID: owner}, subscriptionID)
	require.Error(t, err)
}

func TestPreview_ErrorPaths(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	ctx := context.Background()
	auth := in.AuthContext{AccountID: uuid.NewString()}

	result, err := service.Subscribe(ctx, auth, in.SubscribeInput{AccountID: auth.AccountID, TariffPlanID: testBasicPlan})
	require.NoError(t, err)

	// pending — upgrade запрещён
	_, err = service.PreviewUpgrade(ctx, auth, result.SubscriptionID, testPremiumPlan)
	requireAppError(t, err, CodeInvalidInput)

	payment, _ := store.GetPayment(ctx, result.Payment.PaymentID)
	require.NoError(t, service.HandleWebhook(ctx, successWebhook(t, *payment, "tx-"+uuid.NewString())))

	// нет целевого оффера
	_, err = service.PreviewUpgrade(ctx, auth, result.SubscriptionID, uuid.NewString())
	requireAppError(t, err, CodeInvalidInput)

	// истёкший период
	rec, _ := store.GetSubscription(ctx, result.SubscriptionID)
	rec.PeriodStart = time.Now().AddDate(0, 0, -31)
	rec.PeriodEnd = time.Now().Add(-time.Hour)
	require.NoError(t, store.UpdateSubscription(ctx, *rec))
	_, err = service.PreviewUpgrade(ctx, auth, result.SubscriptionID, testPremiumPlan)
	requireAppError(t, err, CodePeriodEnded)
}

func TestUpgrade_Downgrade_ErrorPaths(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	ctx := context.Background()
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testStandardPlan)
	auth := in.AuthContext{AccountID: accountID}

	// downgrade на дорогой тариф
	_, err := service.Downgrade(ctx, auth, in.DowngradeInput{SubscriptionID: subscriptionID, TargetTariff: testPremiumPlan})
	requireAppError(t, err, CodeInvalidInput)

	// повторный upgrade на текущий тариф
	_, err = service.Upgrade(ctx, auth, in.UpgradeInput{SubscriptionID: subscriptionID, TargetTariff: testStandardPlan})
	requireAppError(t, err, CodeConflict)

	// несуществующая подписка
	_, err = service.Upgrade(ctx, auth, in.UpgradeInput{SubscriptionID: uuid.NewString(), TargetTariff: testPremiumPlan})
	requireAppError(t, err, CodeNotFound)
}

func TestCancelResume_ErrorPaths(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	ctx := context.Background()
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	auth := in.AuthContext{AccountID: accountID}

	_, err := service.Cancel(ctx, auth, subscriptionID)
	require.NoError(t, err)
	_, err = service.Cancel(ctx, auth, subscriptionID)
	requireAppError(t, err, CodeInvalidInput)

	// suspended с истёкшим периодом
	other := subscribeAndActivate(t, service, store, uuid.NewString(), testBasicPlan)
	rec, _ := store.GetSubscription(ctx, other)
	rec.Status = "suspended"
	rec.PeriodStart = time.Now().AddDate(0, 0, -31)
	rec.PeriodEnd = time.Now().Add(-time.Hour)
	require.NoError(t, store.UpdateSubscription(ctx, *rec))
	_, err = service.Resume(ctx, in.AuthContext{AccountID: rec.AccountID}, other)
	requireAppError(t, err, CodePeriodExpired)
}

func TestPatchAndAdmin_ErrorPaths(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	ctx := context.Background()
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	admin := in.AuthContext{Role: "administrator"}
	patient := in.AuthContext{AccountID: accountID}

	_, err := service.PatchSubscription(ctx, patient, in.PatchInput{SubscriptionID: subscriptionID})
	requireAppError(t, err, CodeForbidden)

	view, err := service.PatchSubscription(ctx, admin, in.PatchInput{SubscriptionID: subscriptionID})
	require.NoError(t, err)
	assert.Equal(t, subscriptionID, view.SubscriptionID)

	_, err = service.PatchSubscription(ctx, admin, in.PatchInput{SubscriptionID: uuid.NewString()})
	requireAppError(t, err, CodeNotFound)

	_, err = service.AdminActivateTariff(ctx, patient, in.AdminActivateInput{AccountID: uuid.NewString(), TariffPlanID: testBasicPlan})
	requireAppError(t, err, CodeForbidden)

	_, err = service.AdminActivateTariff(ctx, admin, in.AdminActivateInput{AccountID: uuid.NewString(), TariffPlanID: uuid.NewString()})
	requireAppError(t, err, CodeInvalidInput)

	_, err = service.AdminActivateTariff(ctx, admin, in.AdminActivateInput{AccountID: uuid.NewString(), TariffPlanID: testBasicPlan,
		PeriodStart: ptrTime(time.Now()), PeriodEnd: ptrTime(time.Now().Add(-time.Hour))})
	requireAppError(t, err, CodeInvalidInput)

	_, err = service.AdminDeactivateTariff(ctx, admin, uuid.NewString())
	requireAppError(t, err, CodeNotFound)
}

func TestWebhook_ErrorPaths(t *testing.T) {
	ctx := context.Background()

	t.Run("missing secret", func(t *testing.T) {
		service, _, _, _, _ := newTestService(t)
		service.cfg.WebhookSecret = ""
		requireAppError(t, service.HandleWebhook(ctx, in.WebhookInput{}), CodeForbidden)
	})

	t.Run("stale timestamp", func(t *testing.T) {
		service, store, _, _, _ := newTestService(t)
		accountID := uuid.NewString()
		result, _ := service.Subscribe(ctx, in.AuthContext{AccountID: accountID}, in.SubscribeInput{AccountID: accountID, TariffPlanID: testBasicPlan})
		payment, _ := store.GetPayment(ctx, result.Payment.PaymentID)
		input := successWebhook(t, *payment, "tx-stale")
		input.BatchTimestamp = time.Now().Add(-time.Hour).Unix()
		input.Signature = webhookSignature(input, "secret")
		requireAppError(t, service.HandleWebhook(ctx, input), CodeForbidden)
	})

	t.Run("payment not found", func(t *testing.T) {
		service, _, _, _, _ := newTestService(t)
		input := in.WebhookInput{BatchTimestamp: time.Now().Unix(), CurrencyID: "BYN", Amount: "1.00",
			PaymentMethod: "cc", OrderID: "o", SiteOrderID: uuid.NewString(), TransactionID: "tx-missing", PaymentType: 1}
		input.Signature = webhookSignature(input, "secret")
		requireAppError(t, service.HandleWebhook(ctx, input), CodeNotFound)
	})
}

func TestIdentityEvent_ErrorPaths(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	ctx := context.Background()

	require.Error(t, service.HandleIdentityEvent(ctx, IdentityEvent{EventID: uuid.NewString(),
		EventType: "AccountLockedByAdmin", Payload: json.RawMessage("{bad")}))

	require.Error(t, service.HandleIdentityEvent(ctx, IdentityEvent{EventID: uuid.NewString(),
		EventType: "AccountLockedByAdmin", Payload: json.RawMessage(`{}`)}))

	// неизвестный тип — ack
	require.NoError(t, service.HandleIdentityEvent(ctx, IdentityEvent{EventID: uuid.NewString(),
		EventType: "SessionCreated", Payload: json.RawMessage(`{"accountId":"` + uuid.NewString() + `"}`)}))

	// нет подписки — ack
	require.NoError(t, service.HandleIdentityEvent(ctx, IdentityEvent{EventID: uuid.NewString(),
		EventType: "AccountLockedByAdmin", Payload: json.RawMessage(`{"accountId":"` + uuid.NewString() + `"}`)}))

	store.fail["ProcessedEventExists"] = errors.New("db down")
	require.Error(t, service.HandleIdentityEvent(ctx, IdentityEvent{EventID: uuid.NewString(),
		EventType: "AccountUnlocked", Payload: json.RawMessage(`{"accountId":"` + uuid.NewString() + `"}`)}))
	delete(store.fail, "ProcessedEventExists")
}

func TestLifecycle_ErrorAndRun(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	store.fail["ListSubscriptionsDue"] = errors.New("db down")
	require.Error(t, service.LifecycleTick(context.Background()))
	delete(store.fail, "ListSubscriptionsDue")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		service.RunLifecycle(ctx, time.Millisecond, nil)
		close(done)
	}()
	time.Sleep(5 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunLifecycle did not stop")
	}
}

func TestMetrics_AndErrors(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	metrics := &fakeMetrics{}
	service.SetMetrics(metrics)

	accountID := uuid.NewString()
	result, err := service.Subscribe(context.Background(), in.AuthContext{AccountID: accountID}, in.SubscribeInput{AccountID: accountID, TariffPlanID: testBasicPlan})
	require.NoError(t, err)
	payment, _ := store.GetPayment(context.Background(), result.Payment.PaymentID)
	input := successWebhook(t, *payment, "tx-metrics")
	require.NoError(t, service.HandleWebhook(context.Background(), input))
	input.Signature = "bad"
	require.Error(t, service.HandleWebhook(context.Background(), input))

	assert.NotZero(t, metrics.counts["webhook"])
	assert.NotZero(t, metrics.counts["signature"])
	assert.NotZero(t, metrics.counts["activation"])
	assert.NotZero(t, metrics.counts["payment"])

	err = &Error{Code: CodeInternal, Message: "boom"}
	assert.Contains(t, err.Error(), "boom")
	assert.True(t, isUniqueViolation(errors.New("duplicate key value 23505")))
	assert.False(t, isUniqueViolation(nil))
}

func TestNotifyPreRenewalAndCardExpiry(t *testing.T) {
	service, store, _, _, notifier := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	ctx := context.Background()

	rec, _ := store.GetSubscription(ctx, subscriptionID)
	rec.PeriodStart = time.Now().UTC().Add(-29 * 24 * time.Hour)
	rec.PeriodEnd = time.Now().UTC().Add(24 * time.Hour)
	require.NoError(t, store.UpdateSubscription(ctx, *rec))

	method, _ := store.GetActivePaymentMethod(ctx, subscriptionID)
	soon := time.Now().UTC().AddDate(0, 0, 10)
	method.CardExpDate = &soon
	require.NoError(t, store.UpsertPaymentMethod(ctx, *method))

	require.NoError(t, service.LifecycleTick(ctx))
	kinds := map[string]int{}
	for _, notification := range notifier.sent {
		kinds[notification.Kind]++
	}
	assert.Equal(t, 1, kinds["pre_renewal"])
	assert.Equal(t, 1, kinds["card_expiring"])

	before := len(notifier.sent)
	require.NoError(t, service.LifecycleTick(ctx))
	assert.Equal(t, before, len(notifier.sent), "notifications are deduplicated")
}

func TestUpgrade_NoCardReturnsPaymentLink(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	auth := in.AuthContext{AccountID: accountID}

	require.NoError(t, store.DeactivatePaymentMethods(context.Background(), subscriptionID))
	preview, err := service.PreviewUpgrade(context.Background(), auth, subscriptionID, testPremiumPlan)
	require.NoError(t, err)
	result, err := service.Upgrade(context.Background(), auth, in.UpgradeInput{
		SubscriptionID: subscriptionID, TargetTariff: testPremiumPlan, QuoteID: preview.QuoteID,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, result.AcquiringRedirectURL)
}

func TestIdentityUnlockExpired(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	ctx := context.Background()

	lock := IdentityEvent{EventID: uuid.NewString(), EventType: "AccountLockedByAdmin",
		Payload: json.RawMessage(`{"accountId":"` + accountID + `"}`)}
	require.NoError(t, service.HandleIdentityEvent(ctx, lock))

	rec, _ := store.GetSubscription(ctx, subscriptionID)
	rec.PeriodStart = time.Now().AddDate(0, 0, -31)
	rec.PeriodEnd = time.Now().Add(-time.Hour)
	require.NoError(t, store.UpdateSubscription(ctx, *rec))

	unlock := IdentityEvent{EventID: uuid.NewString(), EventType: "AccountUnlocked",
		Payload: json.RawMessage(`{"accountId":"` + accountID + `"}`)}
	require.NoError(t, service.HandleIdentityEvent(ctx, unlock))
	_, err := store.GetSubscriptionByAccount(ctx, accountID)
	require.ErrorIs(t, err, out.ErrNotFound)
	assert.Contains(t, store.eventTypes(), "SubscriptionExpired")
}

func TestIdentityDelete_NoSubscriptionAndUnbindError(t *testing.T) {
	service, store, _, acquiring, _ := newTestService(t)
	ctx := context.Background()

	// нет подписки — ack
	require.NoError(t, service.HandleIdentityEvent(ctx, IdentityEvent{EventID: uuid.NewString(),
		EventType: "AccountDeleted", Payload: json.RawMessage(`{"accountId":"` + uuid.NewString() + `"}`)}))

	accountID := uuid.NewString()
	subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	acquiring.unbindErr = errors.New("webpay down")
	err := service.HandleIdentityEvent(ctx, IdentityEvent{EventID: uuid.NewString(),
		EventType: "AccountDeleted", Payload: json.RawMessage(`{"accountId":"` + accountID + `"}`)})
	require.Error(t, err, "unbind failure must not delete subscription")
}

func TestAdminActivate_Duplicate(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscribeAndActivate(t, service, store, accountID, testBasicPlan)

	_, err := service.AdminActivateTariff(context.Background(), in.AuthContext{Role: "administrator"}, in.AdminActivateInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
	})
	var appErr *Error
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, CodeActiveSubscription, appErr.Code)
}

func TestWebhook_InitialSuspendedNoActivation(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	result, err := service.Subscribe(context.Background(), in.AuthContext{AccountID: accountID}, in.SubscribeInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
	})
	require.NoError(t, err)

	lock := IdentityEvent{EventID: uuid.NewString(), EventType: "AccountLockedByAdmin",
		Payload: json.RawMessage(`{"accountId":"` + accountID + `"}`)}
	require.NoError(t, service.HandleIdentityEvent(context.Background(), lock))

	payment, _ := store.GetPayment(context.Background(), result.Payment.PaymentID)
	require.NoError(t, service.HandleWebhook(context.Background(), successWebhook(t, *payment, "tx-suspended")))

	rec, _ := store.GetSubscription(context.Background(), result.SubscriptionID)
	assert.Equal(t, "suspended", rec.Status)
	payment, _ = store.GetPayment(context.Background(), result.Payment.PaymentID)
	assert.Equal(t, "success", payment.Status)
	assert.NotContains(t, store.eventTypes(), "SubscriptionActivated")
}

func TestSubscribe_LookupErrorAndDowngradeNoOffer(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	store.fail["GetSubscriptionByAccount"] = errors.New("db down")
	_, err := service.Subscribe(context.Background(), in.AuthContext{AccountID: accountID}, in.SubscribeInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
	})
	require.Error(t, err)
	delete(store.fail, "GetSubscriptionByAccount")

	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	_, err = service.Downgrade(context.Background(), in.AuthContext{AccountID: accountID}, in.DowngradeInput{
		SubscriptionID: subscriptionID, TargetTariff: uuid.NewString(),
	})
	requireAppError(t, err, CodeInvalidInput)
}

func TestWebhook_RenewalFailureRetriesAndExpires(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	ctx := context.Background()

	rec, _ := store.GetSubscription(ctx, subscriptionID)
	rec.PeriodStart = time.Now().UTC().AddDate(0, 0, -31)
	rec.PeriodEnd = time.Now().UTC().Add(-time.Hour)
	require.NoError(t, store.UpdateSubscription(ctx, *rec))

	for attempt := 1; attempt <= 3; attempt++ {
		require.NoError(t, service.LifecycleTick(ctx))
		payments, _ := store.ListPayments(ctx, subscriptionID)
		var renewal *out.PaymentRecord
		for i := range payments {
			if payments[i].Kind == "renewal" && payments[i].Status == "pending" {
				renewal = &payments[i]
			}
		}
		require.NotNil(t, renewal, "attempt %d must create renewal payment", attempt)

		input := successWebhook(t, *renewal, "tx-fail-"+uuid.NewString())
		input.PaymentType = 10
		input.Action = 1
		input.Signature = webhookSignature(input, "secret")
		require.NoError(t, service.HandleWebhook(ctx, input))

		rec, err := store.GetSubscriptionByAccount(ctx, accountID)
		if err != nil {
			break
		}
		assert.Equal(t, attempt, rec.RetryCount)
		rec.NextRetryAt = ptrTime(time.Now().UTC().Add(-time.Second))
		require.NoError(t, store.UpdateSubscription(ctx, *rec))
	}

	_, err := store.GetSubscriptionByAccount(ctx, accountID)
	require.ErrorIs(t, err, out.ErrNotFound)
	events := store.eventTypes()
	assert.Contains(t, events, "PaymentFailed")
	assert.Contains(t, events, "SubscriptionExpired")
	assert.Contains(t, events, "TariffPlanDeActivated")
}

func TestWebhook_ProrationSuccess(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	auth := in.AuthContext{AccountID: accountID}
	ctx := context.Background()

	preview, err := service.PreviewUpgrade(ctx, auth, subscriptionID, testPremiumPlan)
	require.NoError(t, err)
	_, err = service.Upgrade(ctx, auth, in.UpgradeInput{
		SubscriptionID: subscriptionID, TargetTariff: testPremiumPlan, QuoteID: preview.QuoteID,
	})
	require.NoError(t, err)

	payments, _ := store.ListPayments(ctx, subscriptionID)
	var proration *out.PaymentRecord
	for i := range payments {
		if payments[i].Kind == "upgrade_proration" {
			proration = &payments[i]
		}
	}
	require.NotNil(t, proration)
	input := successWebhook(t, *proration, "tx-proration")
	input.PaymentType = 1
	input.OperationType = ""
	input.Signature = webhookSignature(input, "secret")
	require.NoError(t, service.HandleWebhook(ctx, input))

	payment, _ := store.GetPayment(ctx, proration.ID)
	assert.Equal(t, "success", payment.Status)
	assert.Contains(t, store.eventTypes(), "PaymentSucceeded")
}

func TestRenewal_AppliesScheduledDowngrade(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testStandardPlan)
	ctx := context.Background()

	_, err := service.Downgrade(ctx, in.AuthContext{AccountID: accountID}, in.DowngradeInput{
		SubscriptionID: subscriptionID, TargetTariff: testBasicPlan,
	})
	require.NoError(t, err)
	rec, _ := store.GetSubscription(ctx, subscriptionID)
	rec.PeriodStart = time.Now().UTC().AddDate(0, 0, -31)
	rec.PeriodEnd = time.Now().UTC().Add(-time.Hour)
	past := time.Now().UTC().Add(-time.Hour)
	if rec.ScheduledAt != nil {
		rec.ScheduledAt = &past
	}
	require.NoError(t, store.UpdateSubscription(ctx, *rec))

	require.NoError(t, service.LifecycleTick(ctx))
	rec, _ = store.GetSubscription(ctx, subscriptionID)
	assert.Equal(t, testBasicPlan, rec.TariffPlanID)
	payments, _ := store.ListPayments(ctx, subscriptionID)
	var renewal *out.PaymentRecord
	for i := range payments {
		if payments[i].Kind == "renewal" {
			renewal = &payments[i]
		}
	}
	require.NotNil(t, renewal)
	assert.EqualValues(t, 2990, renewal.AmountKopecks)
}

func TestAdminDeactivate_StoreErrorAndHelpers(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	store.fail["UpdateSubscription"] = errors.New("db down")
	_, err := service.AdminDeactivateTariff(context.Background(), in.AuthContext{Role: "administrator"}, accountID)
	require.Error(t, err)
	delete(store.fail, "UpdateSubscription")

	assert.Nil(t, scheduledView(nil))
	assert.EqualValues(t, 0, ceilDiv(1, 0))
}

func TestWebhook_FailureOnSuspendedNoRetry(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	ctx := context.Background()

	lock := IdentityEvent{EventID: uuid.NewString(), EventType: "AccountLockedByAdmin",
		Payload: json.RawMessage(`{"accountId":"` + accountID + `"}`)}
	require.NoError(t, service.HandleIdentityEvent(ctx, lock))

	renewal := out.PaymentRecord{
		ID: uuid.NewString(), SubscriptionID: subscriptionID, AmountKopecks: 2990, Currency: "BYN",
		Status: "pending", Kind: "renewal", TariffPlanName: "Basic", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, store.CreatePayment(ctx, renewal))
	input := successWebhook(t, renewal, "tx-susp-fail")
	input.PaymentType = 10
	input.Action = 1
	input.Signature = webhookSignature(input, "secret")
	require.NoError(t, service.HandleWebhook(ctx, input))

	rec, _ := store.GetSubscription(ctx, subscriptionID)
	assert.Equal(t, "suspended", rec.Status)
	assert.Equal(t, 0, rec.RetryCount, "suspended subscription is not retried")
}

func TestWebhook_SuccessRenewalWhenSuspended(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	ctx := context.Background()

	lock := IdentityEvent{EventID: uuid.NewString(), EventType: "AccountLockedByAdmin",
		Payload: json.RawMessage(`{"accountId":"` + accountID + `"}`)}
	require.NoError(t, service.HandleIdentityEvent(ctx, lock))

	renewal := out.PaymentRecord{
		ID: uuid.NewString(), SubscriptionID: subscriptionID, AmountKopecks: 2990, Currency: "BYN",
		Status: "pending", Kind: "renewal", TariffPlanName: "Basic", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, store.CreatePayment(ctx, renewal))
	input := successWebhook(t, renewal, "tx-susp-ok")
	input.PaymentType = 10
	input.Signature = webhookSignature(input, "secret")
	require.NoError(t, service.HandleWebhook(ctx, input))

	rec, _ := store.GetSubscription(ctx, subscriptionID)
	assert.Equal(t, "suspended", rec.Status)
	assert.NotContains(t, store.eventTypes(), "SubscriptionRenewed")
}

func TestRefund_DuplicateIsIdempotent(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	result, _ := service.Subscribe(context.Background(), in.AuthContext{AccountID: accountID}, in.SubscribeInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
	})
	payment, _ := store.GetPayment(context.Background(), result.Payment.PaymentID)
	require.NoError(t, service.HandleWebhook(context.Background(), successWebhook(t, *payment, "tx-ok")))

	refund := successWebhook(t, *payment, "tx-ref")
	refund.PaymentType = 5
	refund.Signature = webhookSignature(refund, "secret")
	require.NoError(t, service.HandleWebhook(context.Background(), refund))
	require.NoError(t, service.HandleWebhook(context.Background(), refund), "duplicate refund is a no-op")

	payments, _ := store.ListPayments(context.Background(), result.SubscriptionID)
	negatives := 0
	for _, item := range payments {
		if item.AmountKopecks < 0 {
			negatives++
		}
	}
	assert.Equal(t, 1, negatives)
}

func TestAccountDeleted_NoPaymentMethodAndProcessed(t *testing.T) {
	service, store, _, acquiring, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	ctx := context.Background()
	require.NoError(t, store.DeactivatePaymentMethods(ctx, subscriptionID))

	event := IdentityEvent{EventID: uuid.NewString(), EventType: "AccountDeleted",
		Payload: json.RawMessage(`{"accountId":"` + accountID + `"}`)}
	require.NoError(t, service.HandleIdentityEvent(ctx, event))
	assert.Equal(t, 0, acquiring.unbindCalls)
	assert.Len(t, store.archives, 1)
	require.NoError(t, service.HandleIdentityEvent(ctx, event), "duplicate delete is a no-op")
}

func TestAdminActivate_StoreErrors(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	admin := in.AuthContext{Role: "administrator"}

	store.fail["CreateSubscription"] = errors.New("duplicate key 23505")
	_, err := service.AdminActivateTariff(context.Background(), admin, in.AdminActivateInput{
		AccountID: uuid.NewString(), TariffPlanID: testBasicPlan,
	})
	requireAppError(t, err, CodeActiveSubscription)

	store.fail["CreateSubscription"] = errors.New("db down")
	_, err = service.AdminActivateTariff(context.Background(), admin, in.AdminActivateInput{
		AccountID: uuid.NewString(), TariffPlanID: testBasicPlan,
	})
	require.Error(t, err)
}

func TestPatch_ScheduleErrorAndDowngradeStoreErrors(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	admin := in.AuthContext{Role: "administrator"}

	past := time.Now().Add(-time.Hour)
	_, err := service.PatchSubscription(context.Background(), admin, in.PatchInput{
		SubscriptionID: subscriptionID, TargetTariffPlanID: testPremiumPlan, EffectiveAt: &past,
	})
	requireAppError(t, err, CodeInvalidInput)

	store.fail["GetOffer"] = errors.New("db down")
	_, err = service.Downgrade(context.Background(), in.AuthContext{AccountID: accountID}, in.DowngradeInput{
		SubscriptionID: subscriptionID, TargetTariff: testPremiumPlan,
	})
	require.Error(t, err)
}

func TestPreview_CurrencyAndRateLimitErrors(t *testing.T) {
	service, store, cache, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	auth := in.AuthContext{AccountID: accountID}

	offer := store.offers[testPremiumOffer]
	offer.Currency = "USD"
	store.offers[testPremiumOffer] = offer
	_, err := service.PreviewUpgrade(context.Background(), auth, subscriptionID, testPremiumPlan)
	requireAppError(t, err, CodeInvalidInput)

	cache.allowErr = errors.New("redis down")
	_, err = service.Upgrade(context.Background(), auth, in.UpgradeInput{
		SubscriptionID: subscriptionID, TargetTariff: testStandardPlan,
	})
	require.Error(t, err)
}

func TestNotifyPreRenewal_NotifierErrorIgnored(t *testing.T) {
	service, store, _, _, notifier := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	rec, _ := store.GetSubscription(context.Background(), subscriptionID)
	rec.PeriodStart = time.Now().UTC().Add(-29 * 24 * time.Hour)
	rec.PeriodEnd = time.Now().UTC().Add(24 * time.Hour)
	require.NoError(t, store.UpdateSubscription(context.Background(), *rec))

	notifier.err = errors.New("postbox down")
	require.NoError(t, service.LifecycleTick(context.Background()))
}

func TestHelpers_ErrorBranches(t *testing.T) {
	service, store, cache, _, _ := newTestService(t)
	ctx := context.Background()

	// loadOwned/loadAny: ошибка store и битые ID из persistence.
	store.fail["GetSubscription"] = errors.New("db down")
	_, _, err := service.loadOwned(ctx, in.AuthContext{AccountID: uuid.NewString()}, uuid.NewString())
	require.Error(t, err)
	_, _, err = service.loadAny(ctx, uuid.NewString())
	require.Error(t, err)
	delete(store.fail, "GetSubscription")

	bad := out.SubscriptionRecord{ID: "not-a-uuid", AccountID: uuid.NewString(), TariffPlanID: testBasicPlan,
		OfferID: testBasicOffer, Status: "active", WeekBitmap: 127, Timezone: "UTC",
		PeriodStart: time.Now(), PeriodEnd: time.Now().Add(time.Hour)}
	_, err = subscriptionFromRecord(&bad)
	require.Error(t, err)

	// loadQuote: ошибка cache и битый JSON.
	cache.getErr = errors.New("redis down")
	_, err = service.loadQuote(ctx, "q")
	requireAppError(t, err, CodeQuoteExpired)
	cache.getErr = nil
	require.NoError(t, cache.Set(ctx, "quote:bad", []byte("{broken"), time.Minute))
	_, err = service.loadQuote(ctx, "bad")
	requireAppError(t, err, CodeQuoteExpired)

	assert.Equal(t, time.Time{}, parseCardExpDate("bad", time.Time{}))
	assert.Equal(t, uuid.Nil, mustUUID("bad"))
}

func TestLifecycle_ErrorBranches(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	ctx := context.Background()

	for _, method := range []string{"ListStaleRenewalPayments", "ListStaleProrationPayments",
		"ListStalePendingSubscriptions", "ListExpiringPaymentMethods", "ListSubscriptionsEndingBefore"} {
		store.fail[method] = errors.New("db down")
		require.Error(t, service.LifecycleTick(ctx), method)
		delete(store.fail, method)
	}

	// битый payment в reconcileStaleRenewals
	payment := out.PaymentRecord{ID: "bad", SubscriptionID: uuid.NewString(), AmountKopecks: 100,
		Currency: "BYN", Status: "pending", Kind: "renewal", TariffPlanName: "Basic",
		CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now().Add(-time.Hour)}
	store.payments[payment.ID] = payment
	require.Error(t, service.reconcileStaleRenewals(ctx))
	delete(store.payments, payment.ID)

	// reconcileStaleProrations: GetPayment error
	store.fail["GetPayment"] = errors.New("db down")
	store.payments["p1"] = out.PaymentRecord{ID: "p1", SubscriptionID: uuid.NewString(), AmountKopecks: 100,
		Currency: "BYN", Status: "pending", Kind: "upgrade_proration", CreatedAt: time.Now().Add(-time.Hour)}
	require.Error(t, service.reconcileStaleProrations(ctx))
	delete(store.fail, "GetPayment")
	delete(store.payments, "p1")
}

func TestWebhook_StoreErrorBranches(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	result, _ := service.Subscribe(context.Background(), in.AuthContext{AccountID: accountID}, in.SubscribeInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
	})
	payment, _ := store.GetPayment(context.Background(), result.Payment.PaymentID)
	input := successWebhook(t, *payment, "tx-err")

	store.fail["UpdatePayment"] = errors.New("db down")
	require.Error(t, service.HandleWebhook(context.Background(), input))
	delete(store.fail, "UpdatePayment")

	store.fail["GetSubscription"] = errors.New("db down")
	require.Error(t, service.HandleWebhook(context.Background(), input))
	delete(store.fail, "GetSubscription")
}

func TestMetricChange(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	metrics := &fakeMetrics{}
	service.SetMetrics(metrics)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testStandardPlan)

	_, err := service.Downgrade(context.Background(), in.AuthContext{AccountID: accountID}, in.DowngradeInput{
		SubscriptionID: subscriptionID, TargetTariff: testBasicPlan,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, metrics.counts["change"])
}

func TestWebhook_MoreStoreErrors(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	ctx := context.Background()
	accountID := uuid.NewString()
	result, _ := service.Subscribe(ctx, in.AuthContext{AccountID: accountID}, in.SubscribeInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
	})
	payment, _ := store.GetPayment(ctx, result.Payment.PaymentID)

	failure := successWebhook(t, *payment, "tx-fail-err")
	failure.Action = 1
	failure.PaymentType = 2
	failure.Signature = webhookSignature(failure, "secret")

	store.fail["UpdatePayment"] = errors.New("db down")
	require.Error(t, service.HandleWebhook(ctx, failure))
	delete(store.fail, "UpdatePayment")

	store.fail["GetSubscription"] = errors.New("db down")
	require.Error(t, service.HandleWebhook(ctx, failure))
	delete(store.fail, "GetSubscription")

	// refund: ошибка вставки и ошибка чтения платежа
	success := successWebhook(t, *payment, "tx-ok")
	require.NoError(t, service.HandleWebhook(ctx, success))
	refund := successWebhook(t, *payment, "tx-ref-err")
	refund.PaymentType = 5
	refund.Signature = webhookSignature(refund, "secret")
	store.fail["CreatePayment"] = errors.New("db down")
	require.Error(t, service.HandleWebhook(ctx, refund))
	delete(store.fail, "CreatePayment")
	store.fail["GetPayment"] = errors.New("db down")
	require.Error(t, service.HandleWebhook(ctx, refund))
	delete(store.fail, "GetPayment")
}

func TestWebhook_RenewalOfferMissing(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	ctx := context.Background()

	renewal := out.PaymentRecord{ID: uuid.NewString(), SubscriptionID: subscriptionID, AmountKopecks: 2990,
		Currency: "BYN", Status: "pending", Kind: "renewal", TariffPlanName: "Basic",
		CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, store.CreatePayment(ctx, renewal))
	delete(store.offers, testBasicOffer)

	input := successWebhook(t, renewal, "tx-renew-missing")
	input.PaymentType = 10
	input.Signature = webhookSignature(input, "secret")
	require.Error(t, service.HandleWebhook(ctx, input))
}

func TestRenewOne_AndExpire_StoreErrors(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	ctx := context.Background()
	accountID := uuid.NewString()
	now := time.Now().UTC()
	_, err := service.AdminActivateTariff(ctx, in.AuthContext{Role: "administrator"}, in.AdminActivateInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
		PeriodStart: ptrTime(now.AddDate(0, 0, -31)), PeriodEnd: ptrTime(now.AddDate(0, 0, -1)),
	})
	require.NoError(t, err)

	store.fail["CreatePayment"] = errors.New("db down")
	require.Error(t, service.LifecycleTick(ctx))
	delete(store.fail, "CreatePayment")

	// stale pending: ошибка UpdateSubscription
	pendingAccount := uuid.NewString()
	result, _ := service.Subscribe(ctx, in.AuthContext{AccountID: pendingAccount}, in.SubscribeInput{
		AccountID: pendingAccount, TariffPlanID: testBasicPlan,
	})
	rec, _ := store.GetSubscription(ctx, result.SubscriptionID)
	rec.CreatedAt = time.Now().Add(-25 * time.Hour)
	require.NoError(t, store.UpdateSubscription(ctx, *rec))
	store.fail["UpdateSubscription"] = errors.New("db down")
	require.Error(t, service.LifecycleTick(ctx))
	delete(store.fail, "UpdateSubscription")
}

func TestIdentity_LockUpdateError(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	store.fail["UpdateSubscription"] = errors.New("db down")
	err := service.HandleIdentityEvent(context.Background(), IdentityEvent{EventID: uuid.NewString(),
		EventType: "AccountLockedByAdmin", Payload: json.RawMessage(`{"accountId":"` + accountID + `"}`)})
	require.Error(t, err)
}

func TestWebhook_SuccessStoreErrors(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	ctx := context.Background()
	accountID := uuid.NewString()
	result, _ := service.Subscribe(ctx, in.AuthContext{AccountID: accountID}, in.SubscribeInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
	})
	payment, _ := store.GetPayment(ctx, result.Payment.PaymentID)
	input := successWebhook(t, *payment, "tx-ok-err")

	store.fail["UpdatePayment"] = errors.New("db down")
	require.Error(t, service.HandleWebhook(ctx, input))
	delete(store.fail, "UpdatePayment")

	store.fail["GetSubscription"] = errors.New("db down")
	require.Error(t, service.HandleWebhook(ctx, input))
	delete(store.fail, "GetSubscription")
}

func TestReconcileProrations_NoMetadataAndMismatch(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	ctx := context.Background()

	// Без revert-метаданных: только failed.
	legacy := out.PaymentRecord{ID: uuid.NewString(), SubscriptionID: uuid.NewString(), AmountKopecks: 100,
		Currency: "BYN", Status: "pending", Kind: "upgrade_proration", TariffPlanName: "Premium",
		CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now().Add(-time.Hour)}
	require.NoError(t, store.CreatePayment(ctx, legacy))
	require.NoError(t, service.reconcileStaleProrations(ctx))
	payment, _ := store.GetPayment(ctx, legacy.ID)
	assert.Equal(t, "failed", payment.Status)

	// Подписка уже не на applied_offer_id — тариф не откатываем.
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	applied := testPremiumOffer
	revert := testBasicOffer
	revertPlan := testBasicPlan
	bitmap := int16(127)
	tz := "UTC"
	mismatch := out.PaymentRecord{ID: uuid.NewString(), SubscriptionID: subscriptionID, AmountKopecks: 100,
		Currency: "BYN", Status: "pending", Kind: "upgrade_proration", TariffPlanName: "Premium",
		AppliedOfferID: &applied, RevertOfferID: &revert, RevertTariffPlanID: &revertPlan,
		RevertWeekBitmap: &bitmap, RevertTimezone: &tz,
		CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now().Add(-time.Hour)}
	require.NoError(t, store.CreatePayment(ctx, mismatch))
	require.NoError(t, service.reconcileStaleProrations(ctx))

	rec, _ := store.GetSubscription(ctx, subscriptionID)
	assert.Equal(t, testBasicPlan, rec.TariffPlanID)
}

func TestExpire_DBErrors(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	ctx := context.Background()
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)

	rec, _ := store.GetSubscription(ctx, subscriptionID)
	rec.Status = "suspended"
	rec.PeriodStart = time.Now().AddDate(0, 0, -31)
	rec.PeriodEnd = time.Now().Add(-time.Hour)
	require.NoError(t, store.UpdateSubscription(ctx, *rec))

	store.fail["UpdateSubscription"] = errors.New("db down")
	require.Error(t, service.LifecycleTick(ctx))
	delete(store.fail, "UpdateSubscription")
}

func TestIdentity_UnlockActiveAndRenewScheduledMissingOffer(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	ctx := context.Background()
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)

	// unlock активной подписки — no-op
	require.NoError(t, service.HandleIdentityEvent(ctx, IdentityEvent{EventID: uuid.NewString(),
		EventType: "AccountUnlocked", Payload: json.RawMessage(`{"accountId":"` + accountID + `"}`)}))
	rec, _ := store.GetSubscription(ctx, subscriptionID)
	assert.Equal(t, "active", rec.Status)

	// scheduled downgrade, но целевой оффер исчез — воркер возвращает ошибку
	subRec, _ := store.GetSubscription(ctx, subscriptionID)
	target := testStandardPlan
	past := time.Now().Add(-time.Hour)
	subRec.ScheduledTarget = &target
	subRec.ScheduledAt = &past
	subRec.PeriodStart = time.Now().AddDate(0, 0, -31)
	subRec.PeriodEnd = time.Now().Add(-time.Hour)
	require.NoError(t, store.UpdateSubscription(ctx, *subRec))
	delete(store.offers, testStandardOffer)

	require.Error(t, service.LifecycleTick(ctx))
}

func TestAdminDeactivate_InvalidAndStoreErrors(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	ctx := context.Background()
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	admin := in.AuthContext{Role: "administrator"}

	// suspended нельзя отменить через Cancel
	require.NoError(t, service.HandleIdentityEvent(ctx, IdentityEvent{EventID: uuid.NewString(),
		EventType: "AccountLockedByAdmin", Payload: json.RawMessage(`{"accountId":"` + accountID + `"}`)}))
	_, err := service.AdminDeactivateTariff(ctx, admin, accountID)
	require.Error(t, err)

	// store error
	store.fail["GetSubscriptionByAccount"] = errors.New("db down")
	_, err = service.AdminDeactivateTariff(ctx, admin, uuid.NewString())
	require.Error(t, err)
	delete(store.fail, "GetSubscriptionByAccount")
	_ = subscriptionID
}

func TestWebhook_DuplicateSuccessNoNewEvents(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	ctx := context.Background()
	accountID := uuid.NewString()
	result, _ := service.Subscribe(ctx, in.AuthContext{AccountID: accountID}, in.SubscribeInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
	})
	payment, _ := store.GetPayment(ctx, result.Payment.PaymentID)
	input := successWebhook(t, *payment, "tx-dup")
	require.NoError(t, service.HandleWebhook(ctx, input))
	before := len(store.eventTypes())

	require.NoError(t, service.HandleWebhook(ctx, input))
	assert.Equal(t, before, len(store.eventTypes()), "duplicate webhook is a no-op")
}

func TestSubscribe_InvalidOfferPrice(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	offer := store.offers[testBasicOffer]
	offer.PriceKopecks = 0
	store.offers[testBasicOffer] = offer
	_, err := service.Subscribe(context.Background(), in.AuthContext{AccountID: uuid.NewString()}, in.SubscribeInput{
		AccountID: uuid.NewString(), TariffPlanID: testBasicPlan,
	})
	require.Error(t, err)
}

func TestExpireCancelled_DBError(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	ctx := context.Background()
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	_, err := service.Cancel(ctx, in.AuthContext{AccountID: accountID}, subscriptionID)
	require.NoError(t, err)
	rec, _ := store.GetSubscription(ctx, subscriptionID)
	rec.PeriodStart = time.Now().AddDate(0, 0, -31)
	rec.PeriodEnd = time.Now().Add(-time.Hour)
	require.NoError(t, store.UpdateSubscription(ctx, *rec))

	store.fail["UpdateSubscription"] = errors.New("db down")
	require.Error(t, service.LifecycleTick(ctx))
}

func TestMoreUsecaseErrors(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	ctx := context.Background()
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	auth := in.AuthContext{AccountID: accountID}

	// Preview: ошибка каталога
	store.fail["GetOffer"] = errors.New("db down")
	_, err := service.PreviewUpgrade(ctx, auth, subscriptionID, testPremiumPlan)
	require.Error(t, err)
	delete(store.fail, "GetOffer")

	// Cancel/Resume/Patch: ошибка записи
	store.fail["UpdateSubscription"] = errors.New("db down")
	_, err = service.Cancel(ctx, auth, subscriptionID)
	require.Error(t, err)
	_, err = service.PatchSubscription(ctx, in.AuthContext{Role: "administrator"},
		in.PatchInput{SubscriptionID: subscriptionID, TargetTariffPlanID: testPremiumPlan})
	require.Error(t, err)
	delete(store.fail, "UpdateSubscription")

	// Refund: ошибка чтения платежа
	payment, _ := store.GetPayment(ctx, subscriptionID) // not found path ignored below
	_ = payment
	payments, _ := store.ListPayments(ctx, subscriptionID)
	require.NotEmpty(t, payments)
	refundInput := successWebhook(t, payments[0], "tx-ref-read")
	refundInput.PaymentType = 5
	refundInput.Signature = webhookSignature(refundInput, "secret")
	store.fail["GetPayment"] = errors.New("db down")
	require.Error(t, service.HandleWebhook(ctx, refundInput))
	delete(store.fail, "GetPayment")
}

func TestResumeAndDelete_ErrorBranches(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	ctx := context.Background()
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	auth := in.AuthContext{AccountID: accountID}

	lock := IdentityEvent{EventID: uuid.NewString(), EventType: "AccountLockedByAdmin",
		Payload: json.RawMessage(`{"accountId":"` + accountID + `"}`)}
	require.NoError(t, service.HandleIdentityEvent(ctx, lock))

	store.fail["UpdateSubscription"] = errors.New("db down")
	_, err := service.Resume(ctx, auth, subscriptionID)
	require.Error(t, err)
	delete(store.fail, "UpdateSubscription")

	store.fail["GetActivePaymentMethod"] = errors.New("db down")
	err = service.HandleIdentityEvent(ctx, IdentityEvent{EventID: uuid.NewString(), EventType: "AccountDeleted",
		Payload: json.RawMessage(`{"accountId":"` + accountID + `"}`)})
	require.Error(t, err)
	delete(store.fail, "GetActivePaymentMethod")

	store.fail["ArchiveAccount"] = errors.New("db down")
	err = service.HandleIdentityEvent(ctx, IdentityEvent{EventID: uuid.NewString(), EventType: "AccountDeleted",
		Payload: json.RawMessage(`{"accountId":"` + accountID + `"}`)})
	require.Error(t, err)
}

func TestFinalCoverageBranches(t *testing.T) {
	// Битый scheduled target в persistence.
	badScheduled := "not-a-uuid"
	periodStart := time.Now()
	bad := out.SubscriptionRecord{ID: uuid.NewString(), AccountID: uuid.NewString(), TariffPlanID: testBasicPlan,
		OfferID: testBasicOffer, Status: "active", WeekBitmap: 127, Timezone: "UTC",
		PeriodStart: periodStart, PeriodEnd: periodStart.Add(time.Hour), ScheduledTarget: &badScheduled, ScheduledAt: &periodStart}
	_, err := subscriptionFromRecord(&bad)
	require.Error(t, err)

	service, store, _, _, _ := newTestService(t)
	// Невалидный accountId в JWT → домен отклоняет.
	_, err = service.Subscribe(context.Background(), in.AuthContext{AccountID: "not-a-uuid"}, in.SubscribeInput{
		AccountID: "not-a-uuid", TariffPlanID: testBasicPlan,
	})
	require.Error(t, err)

	// Ошибка привязки карты в webhook → транзакция откатывается.
	accountID := uuid.NewString()
	result, _ := service.Subscribe(context.Background(), in.AuthContext{AccountID: accountID}, in.SubscribeInput{
		AccountID: accountID, TariffPlanID: testBasicPlan,
	})
	payment, _ := store.GetPayment(context.Background(), result.Payment.PaymentID)
	store.fail["UpsertPaymentMethod"] = errors.New("db down")
	require.Error(t, service.HandleWebhook(context.Background(), successWebhook(t, *payment, "tx-bind")))
	delete(store.fail, "UpsertPaymentMethod")

	// Каталог недоступен в admin activate / store error в Resume.
	store.fail["FindActiveOffer"] = errors.New("db down")
	_, err = service.AdminActivateTariff(context.Background(), in.AuthContext{Role: "administrator"}, in.AdminActivateInput{
		AccountID: uuid.NewString(), TariffPlanID: testBasicPlan,
	})
	require.Error(t, err)
	delete(store.fail, "FindActiveOffer")

	store.fail["GetSubscription"] = errors.New("db down")
	_, err = service.Resume(context.Background(), in.AuthContext{AccountID: accountID}, result.SubscriptionID)
	require.Error(t, err)
	delete(store.fail, "GetSubscription")

	store.fail["GetPayment"] = errors.New("db down")
	require.Error(t, service.HandleWebhook(context.Background(), successWebhook(t, *payment, "tx-read-err")))
}

func TestFinalSmallBranches(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	ctx := context.Background()
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)

	// Чужой аккаунт: ListPayments → 404.
	_, err := service.ListPayments(ctx, in.AuthContext{AccountID: uuid.NewString()}, subscriptionID)
	requireAppError(t, err, CodeNotFound)

	// Ошибка записи при downgrade.
	store.fail["UpdateSubscription"] = errors.New("db down")
	_, err = service.Downgrade(ctx, in.AuthContext{AccountID: accountID}, in.DowngradeInput{
		SubscriptionID: subscriptionID, TargetTariff: testBasicPlan,
	})
	require.Error(t, err)
	delete(store.fail, "UpdateSubscription")

	// RunLifecycle: ошибка тика логируется и цикл продолжается.
	store.fail["ListSubscriptionsDue"] = errors.New("db down")
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		service.RunLifecycle(runCtx, time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	<-done
	delete(store.fail, "ListSubscriptionsDue")
}

func TestLateSuccessAfterTimeout(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	metrics := &fakeMetrics{}
	service.SetMetrics(metrics)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	auth := in.AuthContext{AccountID: accountID}
	ctx := context.Background()

	preview, err := service.PreviewUpgrade(ctx, auth, subscriptionID, testPremiumPlan)
	require.NoError(t, err)
	_, err = service.Upgrade(ctx, auth, in.UpgradeInput{SubscriptionID: subscriptionID, TargetTariff: testPremiumPlan, QuoteID: preview.QuoteID})
	require.NoError(t, err)

	payments, _ := store.ListPayments(ctx, subscriptionID)
	var proration *out.PaymentRecord
	for i := range payments {
		if payments[i].Kind == "upgrade_proration" {
			proration = &payments[i]
		}
	}
	require.NotNil(t, proration)
	proration.CreatedAt = time.Now().Add(-time.Hour)
	require.NoError(t, store.UpdatePayment(ctx, *proration))
	require.NoError(t, service.LifecycleTick(ctx))

	late := successWebhook(t, *proration, "tx-late")
	late.PaymentType = 1
	late.OperationType = ""
	late.Signature = webhookSignature(late, "secret")
	require.NoError(t, service.HandleWebhook(ctx, late))

	payment, _ := store.GetPayment(ctx, proration.ID)
	assert.Equal(t, "failed", payment.Status, "failed is final")
	assert.Equal(t, "tx-late", payment.ExternalReferenceID, "provider transaction persisted")
	assert.Equal(t, 1, metrics.counts["late_success"])
}

func TestProrationNoTokenBindsCard(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	auth := in.AuthContext{AccountID: accountID}
	ctx := context.Background()

	require.NoError(t, store.DeactivatePaymentMethods(ctx, subscriptionID))
	preview, err := service.PreviewUpgrade(ctx, auth, subscriptionID, testPremiumPlan)
	require.NoError(t, err)
	result, err := service.Upgrade(ctx, auth, in.UpgradeInput{SubscriptionID: subscriptionID, TargetTariff: testPremiumPlan, QuoteID: preview.QuoteID})
	require.NoError(t, err)
	require.NotEmpty(t, result.AcquiringRedirectURL)

	payments, _ := store.ListPayments(ctx, subscriptionID)
	var proration *out.PaymentRecord
	for i := range payments {
		if payments[i].Kind == "upgrade_proration" {
			proration = &payments[i]
		}
	}
	require.NotNil(t, proration)
	input := successWebhook(t, *proration, "tx-bind-proration")
	input.OperationType = "recurring_bind"
	input.RecurringToken = "token-bind"
	input.Signature = webhookSignature(input, "secret")
	require.NoError(t, service.HandleWebhook(ctx, input))

	method, err := store.GetActivePaymentMethod(ctx, subscriptionID)
	require.NoError(t, err)
	assert.Equal(t, "token-bind", method.RecurringToken)
}

func TestUpgrade_RevertStoreError(t *testing.T) {
	service, store, _, acquiring, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	auth := in.AuthContext{AccountID: accountID}
	preview, err := service.PreviewUpgrade(context.Background(), auth, subscriptionID, testPremiumPlan)
	require.NoError(t, err)

	acquiring.failRecurring = true
	store.fail["UpdatePayment"] = errors.New("db down")
	_, err = service.Upgrade(context.Background(), auth, in.UpgradeInput{
		SubscriptionID: subscriptionID, TargetTariff: testPremiumPlan, QuoteID: preview.QuoteID,
	})
	require.Error(t, err)
}

func TestRevertUpgrade_WrongAppliedOfferIsNoop(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	auth := in.AuthContext{AccountID: accountID}
	ctx := context.Background()
	preview, err := service.PreviewUpgrade(ctx, auth, subscriptionID, testPremiumPlan)
	require.NoError(t, err)
	_, err = service.Upgrade(ctx, auth, in.UpgradeInput{SubscriptionID: subscriptionID, TargetTariff: testPremiumPlan, QuoteID: preview.QuoteID})
	require.NoError(t, err)

	payments, _ := store.ListPayments(ctx, subscriptionID)
	var proration *out.PaymentRecord
	for i := range payments {
		if payments[i].Kind == "upgrade_proration" {
			proration = &payments[i]
		}
	}
	require.NotNil(t, proration)

	reverted, err := service.revertUpgrade(ctx, subscriptionID, mustUUID(testBasicPlan), mustUUID(testBasicOffer),
		domain.TariffSnapshot{WeekBitmap: 127, Timezone: "UTC"}, uuid.New(), mustUUID(proration.ID))
	require.NoError(t, err)
	assert.False(t, reverted)
}

func TestAdminActivate_ExplicitStartOnly(t *testing.T) {
	service, _, _, _, _ := newTestService(t)
	start := time.Now().UTC().Add(-time.Hour)
	result, err := service.AdminActivateTariff(context.Background(), in.AuthContext{Role: "administrator"}, in.AdminActivateInput{
		AccountID: uuid.NewString(), TariffPlanID: testBasicPlan, PeriodStart: &start,
	})
	require.NoError(t, err)
	assert.Equal(t, "active", result.Status)
}

func TestFinalCoverageBranches2(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	ctx := context.Background()

	// loadOwned: битая запись из БД.
	badID := uuid.NewString()
	store.subscriptions[badID] = out.SubscriptionRecord{ID: badID, AccountID: uuid.NewString(),
		TariffPlanID: testBasicPlan, OfferID: testBasicOffer, Status: "active", WeekBitmap: 127, Timezone: "UTC",
		PeriodStart: time.Now(), PeriodEnd: time.Now().Add(-time.Hour)}
	_, err := service.GetSubscription(ctx, in.AuthContext{AccountID: "x"}, badID)
	require.Error(t, err)
	delete(store.subscriptions, badID)

	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testStandardPlan)

	// Downgrade: ошибка чтения подписки.
	store.fail["GetSubscription"] = errors.New("db down")
	_, err = service.Downgrade(ctx, in.AuthContext{AccountID: accountID}, in.DowngradeInput{
		SubscriptionID: subscriptionID, TargetTariff: testBasicPlan,
	})
	require.Error(t, err)
	// renewOne: ошибка чтения подписки
	require.Error(t, service.renewOne(ctx, subscriptionID, time.Now()))
	delete(store.fail, "GetSubscription")

	// lock: ошибка projection account_status
	store.fail["UpsertAccountStatus"] = errors.New("db down")
	err = service.HandleIdentityEvent(ctx, IdentityEvent{EventID: uuid.NewString(), EventType: "AccountLockedByAdmin",
		Payload: json.RawMessage(`{"accountId":"` + accountID + `"}`)})
	require.Error(t, err)
}

func TestRevertUpgrade_PaymentSuccessIsNoop(t *testing.T) {
	service, store, _, _, _ := newTestService(t)
	accountID := uuid.NewString()
	subscriptionID := subscribeAndActivate(t, service, store, accountID, testBasicPlan)
	auth := in.AuthContext{AccountID: accountID}
	ctx := context.Background()
	preview, err := service.PreviewUpgrade(ctx, auth, subscriptionID, testPremiumPlan)
	require.NoError(t, err)
	_, err = service.Upgrade(ctx, auth, in.UpgradeInput{SubscriptionID: subscriptionID, TargetTariff: testPremiumPlan, QuoteID: preview.QuoteID})
	require.NoError(t, err)

	payments, _ := store.ListPayments(ctx, subscriptionID)
	var proration *out.PaymentRecord
	for i := range payments {
		if payments[i].Kind == "upgrade_proration" {
			proration = &payments[i]
		}
	}
	require.NotNil(t, proration)
	proration.Status = "success"
	require.NoError(t, store.UpdatePayment(ctx, *proration))

	reverted, err := service.revertUpgrade(ctx, subscriptionID, mustUUID(testBasicPlan), mustUUID(testBasicOffer),
		domain.TariffSnapshot{WeekBitmap: 127, Timezone: "UTC"}, mustUUID(testPremiumOffer), mustUUID(proration.ID))
	require.NoError(t, err)
	assert.False(t, reverted)
	rec, _ := store.GetSubscription(ctx, subscriptionID)
	assert.Equal(t, testPremiumPlan, rec.TariffPlanID)
}

func requireAppError(t *testing.T, err error, code string) {
	t.Helper()
	var appErr *Error
	require.True(t, errors.As(err, &appErr), "want app error, got %v", err)
	assert.Equal(t, code, appErr.Code)
}

var _ = out.ErrNotFound
