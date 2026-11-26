package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/Reddetk/SayMoDev/billing-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/billing-service/internal/usecase"
)

type fakeBilling struct {
	subscribe       func(context.Context, in.AuthContext, in.SubscribeInput) (in.SubscribeResult, error)
	get             func(context.Context, in.AuthContext, string) (in.SubscriptionView, error)
	preview         func(context.Context, in.AuthContext, string, string) (in.UpgradePreview, error)
	upgrade         func(context.Context, in.AuthContext, in.UpgradeInput) (in.UpgradeResult, error)
	downgrade       func(context.Context, in.AuthContext, in.DowngradeInput) (in.DowngradeResult, error)
	cancel          func(context.Context, in.AuthContext, string) (in.CancelResult, error)
	resume          func(context.Context, in.AuthContext, string) (in.ResumeResult, error)
	payments        func(context.Context, in.AuthContext, string) ([]in.PaymentView, error)
	webhook         func(context.Context, in.WebhookInput) error
	patch           func(context.Context, in.AuthContext, in.PatchInput) (in.SubscriptionView, error)
	adminActivate   func(context.Context, in.AuthContext, in.AdminActivateInput) (in.AdminActivateResult, error)
	adminDeactivate func(context.Context, in.AuthContext, string) (in.AdminDeactivateResult, error)
}

func (f *fakeBilling) Subscribe(ctx context.Context, a in.AuthContext, i in.SubscribeInput) (in.SubscribeResult, error) {
	return f.subscribe(ctx, a, i)
}
func (f *fakeBilling) GetSubscription(ctx context.Context, a in.AuthContext, id string) (in.SubscriptionView, error) {
	return f.get(ctx, a, id)
}
func (f *fakeBilling) PreviewUpgrade(ctx context.Context, a in.AuthContext, id, target string) (in.UpgradePreview, error) {
	return f.preview(ctx, a, id, target)
}
func (f *fakeBilling) Upgrade(ctx context.Context, a in.AuthContext, i in.UpgradeInput) (in.UpgradeResult, error) {
	return f.upgrade(ctx, a, i)
}
func (f *fakeBilling) Downgrade(ctx context.Context, a in.AuthContext, i in.DowngradeInput) (in.DowngradeResult, error) {
	return f.downgrade(ctx, a, i)
}
func (f *fakeBilling) Cancel(ctx context.Context, a in.AuthContext, id string) (in.CancelResult, error) {
	return f.cancel(ctx, a, id)
}
func (f *fakeBilling) Resume(ctx context.Context, a in.AuthContext, id string) (in.ResumeResult, error) {
	return f.resume(ctx, a, id)
}
func (f *fakeBilling) ListPayments(ctx context.Context, a in.AuthContext, id string) ([]in.PaymentView, error) {
	return f.payments(ctx, a, id)
}
func (f *fakeBilling) HandleWebhook(ctx context.Context, i in.WebhookInput) error {
	return f.webhook(ctx, i)
}
func (f *fakeBilling) PatchSubscription(ctx context.Context, a in.AuthContext, i in.PatchInput) (in.SubscriptionView, error) {
	return f.patch(ctx, a, i)
}
func (f *fakeBilling) AdminActivateTariff(ctx context.Context, a in.AuthContext, i in.AdminActivateInput) (in.AdminActivateResult, error) {
	return f.adminActivate(ctx, a, i)
}
func (f *fakeBilling) AdminDeactivateTariff(ctx context.Context, a in.AuthContext, id string) (in.AdminDeactivateResult, error) {
	return f.adminDeactivate(ctx, a, id)
}

func notImplemented[T any](t *testing.T) func() (T, error) {
	return func() (T, error) {
		t.Helper()
		t.Fatal("handler called unexpected service method")
		var zero T
		return zero, nil
	}
}

func newHandlerContext(method, path, body string, params gin.Params, auth in.AuthContext) (*BillingHandler, *gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	ctx.Request = request
	ctx.Params = params
	ctx.Set(authContextKey, auth)
	service := &fakeBilling{}
	return NewBillingHandler(service), ctx, recorder
}

func TestHandler_Subscribe(t *testing.T) {
	accountID := uuid.NewString()
	h, c, w := newHandlerContext(http.MethodPost, "/billing/subscriptions",
		`{"tariffPlanId":"`+uuid.NewString()+`","paymentMethod":{"type":"card"}}`, nil,
		in.AuthContext{AccountID: accountID})
	fake := h.service.(*fakeBilling)
	fake.subscribe = func(_ context.Context, auth in.AuthContext, input in.SubscribeInput) (in.SubscribeResult, error) {
		assert.Equal(t, accountID, auth.AccountID)
		assert.NotEmpty(t, input.TariffPlanID)
		return in.SubscribeResult{SubscriptionID: uuid.NewString(), Status: "pending"}, nil
	}
	h.Subscribe(c)
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Contains(t, w.Body.String(), "pending")

	// bad JSON
	h, c, w = newHandlerContext(http.MethodPost, "/billing/subscriptions", `{bad`, nil, in.AuthContext{AccountID: accountID})
	h.Subscribe(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// invalid tariff UUID
	h, c, w = newHandlerContext(http.MethodPost, "/billing/subscriptions", `{"tariffPlanId":"bad"}`, nil, in.AuthContext{AccountID: accountID})
	h.Subscribe(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// service error
	h, c, w = newHandlerContext(http.MethodPost, "/billing/subscriptions",
		`{"tariffPlanId":"`+uuid.NewString()+`"}`, nil, in.AuthContext{AccountID: accountID})
	h.service.(*fakeBilling).subscribe = func(context.Context, in.AuthContext, in.SubscribeInput) (in.SubscribeResult, error) {
		return in.SubscribeResult{}, usecase.NewError(usecase.CodeActiveSubscription, "exists")
	}
	h.Subscribe(c)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestHandler_GetAndPayments(t *testing.T) {
	id := uuid.NewString()
	auth := in.AuthContext{AccountID: uuid.NewString()}

	h, c, w := newHandlerContext(http.MethodGet, "/billing/subscriptions/"+id, "", gin.Params{{Key: "subscriptionId", Value: id}}, auth)
	fake := h.service.(*fakeBilling)
	fake.get = func(context.Context, in.AuthContext, string) (in.SubscriptionView, error) {
		return in.SubscriptionView{SubscriptionID: id, Status: "active"}, nil
	}
	h.GetSubscription(c)
	assert.Equal(t, http.StatusOK, w.Code)

	h, c, w = newHandlerContext(http.MethodGet, "/billing/subscriptions/bad", "", gin.Params{{Key: "subscriptionId", Value: "bad"}}, auth)
	h.GetSubscription(c)
	assert.Equal(t, http.StatusNotFound, w.Code)

	h, c, w = newHandlerContext(http.MethodGet, "/billing/subscriptions/"+id, "", gin.Params{{Key: "subscriptionId", Value: id}}, auth)
	fake = h.service.(*fakeBilling)
	fake.payments = func(context.Context, in.AuthContext, string) ([]in.PaymentView, error) {
		return []in.PaymentView{{PaymentID: uuid.NewString(), Status: "pending"}}, nil
	}
	h.ListPayments(c)
	assert.Equal(t, http.StatusOK, w.Code)

	h, c, w = newHandlerContext(http.MethodGet, "/billing/subscriptions/bad/payments", "", gin.Params{{Key: "subscriptionId", Value: "bad"}}, auth)
	h.ListPayments(c)
	assert.Equal(t, http.StatusNotFound, w.Code)

	h, c, w = newHandlerContext(http.MethodGet, "/billing/subscriptions/"+id+"/payments", "", gin.Params{{Key: "subscriptionId", Value: id}}, auth)
	h.service.(*fakeBilling).payments = func(context.Context, in.AuthContext, string) ([]in.PaymentView, error) {
		return nil, usecase.NewError(usecase.CodeInternal, "boom")
	}
	h.ListPayments(c)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandler_PreviewAndUpgrade(t *testing.T) {
	id := uuid.NewString()
	target := uuid.NewString()
	auth := in.AuthContext{AccountID: uuid.NewString()}

	h, c, w := newHandlerContext(http.MethodGet, "/billing/subscriptions/"+id+"/upgrade/preview?targetTariffPlanId="+target, "",
		gin.Params{{Key: "subscriptionId", Value: id}}, auth)
	c.Request.URL.RawQuery = "targetTariffPlanId=" + target
	fake := h.service.(*fakeBilling)
	fake.preview = func(context.Context, in.AuthContext, string, string) (in.UpgradePreview, error) {
		return in.UpgradePreview{QuoteID: "q", QuoteExpiresAt: time.Now()}, nil
	}
	h.PreviewUpgrade(c)
	assert.Equal(t, http.StatusOK, w.Code)

	// invalid target
	h, c, w = newHandlerContext(http.MethodGet, "/billing/subscriptions/"+id+"/upgrade/preview", "", gin.Params{{Key: "subscriptionId", Value: id}}, auth)
	h.PreviewUpgrade(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// upgrade success
	h, c, w = newHandlerContext(http.MethodPost, "/billing/subscriptions/"+id+"/upgrade",
		`{"targetTariffPlanId":"`+target+`","quoteId":"q"}`, gin.Params{{Key: "subscriptionId", Value: id}}, auth)
	h.service.(*fakeBilling).upgrade = func(context.Context, in.AuthContext, in.UpgradeInput) (in.UpgradeResult, error) {
		return in.UpgradeResult{SubscriptionID: id, Status: "active", ProrationCharge: in.ProrationCharge{AmountKopecks: 1}}, nil
	}
	h.Upgrade(c)
	assert.Equal(t, http.StatusOK, w.Code)

	// invalid body
	h, c, w = newHandlerContext(http.MethodPost, "/billing/subscriptions/"+id+"/upgrade", `{"targetTariffPlanId":"bad"}`,
		gin.Params{{Key: "subscriptionId", Value: id}}, auth)
	h.Upgrade(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// quote expired
	h, c, w = newHandlerContext(http.MethodPost, "/billing/subscriptions/"+id+"/upgrade",
		`{"targetTariffPlanId":"`+target+`"}`, gin.Params{{Key: "subscriptionId", Value: id}}, auth)
	h.service.(*fakeBilling).upgrade = func(context.Context, in.AuthContext, in.UpgradeInput) (in.UpgradeResult, error) {
		return in.UpgradeResult{}, &in.QuoteExpiredError{Refreshed: in.UpgradePreview{QuoteID: "new"}}
	}
	h.Upgrade(c)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "refreshedPreview")
}

func TestHandler_DowngradeCancelResume(t *testing.T) {
	id := uuid.NewString()
	auth := in.AuthContext{AccountID: uuid.NewString()}
	target := uuid.NewString()

	h, c, w := newHandlerContext(http.MethodPost, "/billing/subscriptions/"+id+"/downgrade",
		`{"targetTariffPlanId":"`+target+`"}`, gin.Params{{Key: "subscriptionId", Value: id}}, auth)
	h.service.(*fakeBilling).downgrade = func(context.Context, in.AuthContext, in.DowngradeInput) (in.DowngradeResult, error) {
		return in.DowngradeResult{SubscriptionID: id, Status: "active"}, nil
	}
	h.Downgrade(c)
	assert.Equal(t, http.StatusOK, w.Code)

	h, c, w = newHandlerContext(http.MethodPost, "/billing/subscriptions/"+id+"/downgrade", `{}`,
		gin.Params{{Key: "subscriptionId", Value: id}}, auth)
	h.Downgrade(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	h, c, w = newHandlerContext(http.MethodPost, "/billing/subscriptions/"+id+"/cancel", "",
		gin.Params{{Key: "subscriptionId", Value: id}}, auth)
	h.service.(*fakeBilling).cancel = func(context.Context, in.AuthContext, string) (in.CancelResult, error) {
		return in.CancelResult{SubscriptionID: id, Status: "cancelled"}, nil
	}
	h.Cancel(c)
	assert.Equal(t, http.StatusOK, w.Code)

	h, c, w = newHandlerContext(http.MethodPost, "/billing/subscriptions/bad/cancel", "",
		gin.Params{{Key: "subscriptionId", Value: "bad"}}, auth)
	h.Cancel(c)
	assert.Equal(t, http.StatusNotFound, w.Code)

	h, c, w = newHandlerContext(http.MethodPost, "/billing/subscriptions/"+id+"/resume", "",
		gin.Params{{Key: "subscriptionId", Value: id}}, auth)
	h.service.(*fakeBilling).resume = func(context.Context, in.AuthContext, string) (in.ResumeResult, error) {
		return in.ResumeResult{}, usecase.NewError(usecase.CodeNotSuspended, "not suspended")
	}
	h.Resume(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_Webhook(t *testing.T) {
	h, c, w := newHandlerContext(http.MethodPost, "/billing/webhooks/acquiring",
		"batch_timestamp=1&payment_type=1&action=0&site_order_id="+uuid.NewString()+"&signature=abc",
		nil, in.AuthContext{})
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var captured in.WebhookInput
	h.service.(*fakeBilling).webhook = func(_ context.Context, input in.WebhookInput) error {
		captured = input
		return nil
	}
	h.AcquiringWebhook(c)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(1), captured.BatchTimestamp)
	assert.Equal(t, 1, captured.PaymentType)
	assert.Equal(t, 0, captured.Action)
	assert.NotEmpty(t, captured.SiteOrderID)
	assert.Equal(t, "abc", captured.Signature, "signature alias is accepted")

	h, c, w = newHandlerContext(http.MethodPost, "/billing/webhooks/acquiring", "x=1", nil, in.AuthContext{})
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.service.(*fakeBilling).webhook = func(context.Context, in.WebhookInput) error {
		return usecase.NewError(usecase.CodeForbidden, "bad signature")
	}
	h.AcquiringWebhook(c)
	assert.Equal(t, http.StatusUnauthorized, w.Code, "webhook auth failures are 401")

	h, c, w = newHandlerContext(http.MethodPost, "/billing/webhooks/acquiring", "x=1", nil, in.AuthContext{})
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.service.(*fakeBilling).webhook = func(context.Context, in.WebhookInput) error {
		return usecase.NewError(usecase.CodeNotFound, "payment not found")
	}
	h.AcquiringWebhook(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandler_PatchAndAdmin(t *testing.T) {
	id := uuid.NewString()
	target := uuid.NewString()
	accountID := uuid.NewString()
	admin := in.AuthContext{Role: "administrator"}

	h, c, w := newHandlerContext(http.MethodPatch, "/billing/subscriptions/"+id,
		`{"scheduledChange":{"targetTariffPlanId":"`+target+`"}}`,
		gin.Params{{Key: "subscriptionId", Value: id}}, admin)
	h.service.(*fakeBilling).patch = func(context.Context, in.AuthContext, in.PatchInput) (in.SubscriptionView, error) {
		return in.SubscriptionView{SubscriptionID: id, Status: "active"}, nil
	}
	h.PatchSubscription(c)
	assert.Equal(t, http.StatusOK, w.Code)

	// no scheduledChange
	h, c, w = newHandlerContext(http.MethodPatch, "/billing/subscriptions/"+id, `{}`,
		gin.Params{{Key: "subscriptionId", Value: id}}, admin)
	h.PatchSubscription(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// invalid JSON
	h, c, w = newHandlerContext(http.MethodPatch, "/billing/subscriptions/"+id, `{bad`,
		gin.Params{{Key: "subscriptionId", Value: id}}, admin)
	h.PatchSubscription(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// admin activate success
	h, c, w = newHandlerContext(http.MethodPost, "/admin/billing/accounts/"+accountID+"/activate-tariff",
		`{"tariffPlanId":"`+target+`"}`, gin.Params{{Key: "accountId", Value: accountID}}, admin)
	h.service.(*fakeBilling).adminActivate = func(context.Context, in.AuthContext, in.AdminActivateInput) (in.AdminActivateResult, error) {
		return in.AdminActivateResult{SubscriptionID: id, Status: "active"}, nil
	}
	h.AdminActivateTariff(c)
	assert.Equal(t, http.StatusCreated, w.Code)

	h, c, w = newHandlerContext(http.MethodPost, "/admin/billing/accounts/bad/activate-tariff",
		`{"tariffPlanId":"`+target+`"}`, gin.Params{{Key: "accountId", Value: "bad"}}, admin)
	h.AdminActivateTariff(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	h, c, w = newHandlerContext(http.MethodPost, "/admin/billing/accounts/"+accountID+"/deactivate-tariff", "",
		gin.Params{{Key: "accountId", Value: accountID}}, admin)
	h.service.(*fakeBilling).adminDeactivate = func(context.Context, in.AuthContext, string) (in.AdminDeactivateResult, error) {
		return in.AdminDeactivateResult{SubscriptionID: id, Status: "cancelled"}, nil
	}
	h.AdminDeactivateTariff(c)
	assert.Equal(t, http.StatusOK, w.Code)

	h, c, w = newHandlerContext(http.MethodPost, "/admin/billing/accounts/bad/deactivate-tariff", "",
		gin.Params{{Key: "accountId", Value: "bad"}}, admin)
	h.AdminDeactivateTariff(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRespondError_Mapping(t *testing.T) {
	cases := map[string]int{
		usecase.CodeInvalidInput:          400,
		usecase.CodeNotFound:              404,
		usecase.CodeForbidden:             403,
		usecase.CodeAccountInactive:       403,
		usecase.CodeActiveSubscription:    409,
		usecase.CodeIdempotencyInProgress: 409,
		usecase.CodeConflict:              409,
		usecase.CodeRateLimit:             429,
		usecase.CodePaymentFailed:         400,
		usecase.CodeInternal:              500,
	}
	for code, status := range cases {
		gin.SetMode(gin.TestMode)
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		respondError(ctx, usecase.NewError(code, "x"))
		assert.Equal(t, status, recorder.Code, code)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	respondError(ctx, errors.New("plain"))
	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "internal_error")
}

func TestHelpersUUID(t *testing.T) {
	assert.True(t, validUUID(uuid.NewString()))
	assert.False(t, validUUID("bad"))
	assert.Equal(t, "a", firstNonEmpty("", "a", "b"))
	assert.Empty(t, firstNonEmpty("", ""))
}
