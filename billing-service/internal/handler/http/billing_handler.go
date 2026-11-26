package httpapi

// goca:manual — HTTP-контракты BC#2 (endpoints.md#bc2), gin.

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Reddetk/SayMoDev/billing-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/billing-service/internal/usecase"
)

// BillingHandler — все бизнес-ручки BC#2.
type BillingHandler struct {
	service in.BillingService
}

// NewBillingHandler создаёт handler.
func NewBillingHandler(service in.BillingService) *BillingHandler {
	return &BillingHandler{service: service}
}

type subscribeRequest struct {
	TariffPlanID  string `json:"tariffPlanId"`
	PaymentMethod struct {
		Type string `json:"type"`
	} `json:"paymentMethod"`
}

// Subscribe godoc
// @Summary      Активировать подписку
// @Description  Создаёт pending-подписку и pending-платёж, возвращает ссылку WebPay.
// @Tags         billing
// @Accept       json
// @Produce      json
// @Param        Idempotency-Key header string true "UUID"
// @Param        request body subscribeRequest true "tariffPlanId"
// @Success      201 {object} in.SubscribeResult
// @Failure      400,401,403,409 {object} ErrorResponse
// @Security     BearerAuth
// @Router       /billing/subscriptions [post]
func (h *BillingHandler) Subscribe(c *gin.Context) {
	var request subscribeRequest
	if err := c.ShouldBindJSON(&request); err != nil || !validUUID(request.TariffPlanID) {
		respondError(c, usecase.NewError(usecase.CodeInvalidInput, "tariffPlanId is required"))
		return
	}
	result, err := h.service.Subscribe(c.Request.Context(), authFrom(c), in.SubscribeInput{
		AccountID:      authFrom(c).AccountID,
		TariffPlanID:   request.TariffPlanID,
		PaymentType:    request.PaymentMethod.Type,
		IdempotencyKey: c.GetHeader("Idempotency-Key"),
	})
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

// GetSubscription godoc
// @Summary      Подписка по ID
// @Tags         billing
// @Produce      json
// @Param        subscriptionId path string true "UUID"
// @Success      200 {object} in.SubscriptionView
// @Failure      404 {object} ErrorResponse
// @Security     BearerAuth
// @Router       /billing/subscriptions/{subscriptionId} [get]
func (h *BillingHandler) GetSubscription(c *gin.Context) {
	if !validUUID(c.Param("subscriptionId")) {
		respondError(c, usecase.NewError(usecase.CodeNotFound, "subscription not found"))
		return
	}
	view, err := h.service.GetSubscription(c.Request.Context(), authFrom(c), c.Param("subscriptionId"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// PreviewUpgrade godoc
// @Summary      Preview доплаты при upgrade
// @Tags         billing
// @Produce      json
// @Param        subscriptionId path string true "UUID"
// @Param        targetTariffPlanId query string true "UUID"
// @Success      200 {object} in.UpgradePreview
// @Failure      400,404,429 {object} ErrorResponse
// @Security     BearerAuth
// @Router       /billing/subscriptions/{subscriptionId}/upgrade/preview [get]
func (h *BillingHandler) PreviewUpgrade(c *gin.Context) {
	if !validUUID(c.Param("subscriptionId")) {
		respondError(c, usecase.NewError(usecase.CodeNotFound, "subscription not found"))
		return
	}
	target := c.Query("targetTariffPlanId")
	if !validUUID(target) {
		respondError(c, usecase.NewError(usecase.CodeInvalidInput, "targetTariffPlanId is required"))
		return
	}
	preview, err := h.service.PreviewUpgrade(c.Request.Context(), authFrom(c), c.Param("subscriptionId"), target)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, preview)
}

type upgradeRequest struct {
	TargetTariffPlanID string `json:"targetTariffPlanId"`
	QuoteID            string `json:"quoteId"`
}

// Upgrade godoc
// @Summary      Немедленный upgrade с proration
// @Tags         billing
// @Accept       json
// @Produce      json
// @Param        subscriptionId path string true "UUID"
// @Param        Idempotency-Key header string true "UUID"
// @Param        request body upgradeRequest true "targetTariffPlanId"
// @Success      200 {object} in.UpgradeResult
// @Failure      400,404,409,429 {object} ErrorResponse
// @Security     BearerAuth
// @Router       /billing/subscriptions/{subscriptionId}/upgrade [post]
func (h *BillingHandler) Upgrade(c *gin.Context) {
	var request upgradeRequest
	if err := c.ShouldBindJSON(&request); err != nil || !validUUID(request.TargetTariffPlanID) {
		respondError(c, usecase.NewError(usecase.CodeInvalidInput, "targetTariffPlanId is required"))
		return
	}
	if !validUUID(c.Param("subscriptionId")) {
		respondError(c, usecase.NewError(usecase.CodeNotFound, "subscription not found"))
		return
	}
	result, err := h.service.Upgrade(c.Request.Context(), authFrom(c), in.UpgradeInput{
		AccountID:      authFrom(c).AccountID,
		SubscriptionID: c.Param("subscriptionId"),
		TargetTariff:   request.TargetTariffPlanID,
		QuoteID:        request.QuoteID,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

type downgradeRequest struct {
	TargetTariffPlanID string `json:"targetTariffPlanId"`
}

// Downgrade godoc
// @Summary      Отложенный downgrade
// @Tags         billing
// @Accept       json
// @Produce      json
// @Param        subscriptionId path string true "UUID"
// @Param        Idempotency-Key header string true "UUID"
// @Success      200 {object} in.DowngradeResult
// @Security     BearerAuth
// @Router       /billing/subscriptions/{subscriptionId}/downgrade [post]
func (h *BillingHandler) Downgrade(c *gin.Context) {
	var request downgradeRequest
	if err := c.ShouldBindJSON(&request); err != nil || !validUUID(request.TargetTariffPlanID) {
		respondError(c, usecase.NewError(usecase.CodeInvalidInput, "targetTariffPlanId is required"))
		return
	}
	if !validUUID(c.Param("subscriptionId")) {
		respondError(c, usecase.NewError(usecase.CodeNotFound, "subscription not found"))
		return
	}
	result, err := h.service.Downgrade(c.Request.Context(), authFrom(c), in.DowngradeInput{
		AccountID:      authFrom(c).AccountID,
		SubscriptionID: c.Param("subscriptionId"),
		TargetTariff:   request.TargetTariffPlanID,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// Cancel godoc
// @Summary      Отменить подписку
// @Tags         billing
// @Produce      json
// @Param        subscriptionId path string true "UUID"
// @Success      200 {object} in.CancelResult
// @Security     BearerAuth
// @Router       /billing/subscriptions/{subscriptionId}/cancel [post]
func (h *BillingHandler) Cancel(c *gin.Context) {
	if !validUUID(c.Param("subscriptionId")) {
		respondError(c, usecase.NewError(usecase.CodeNotFound, "subscription not found"))
		return
	}
	result, err := h.service.Cancel(c.Request.Context(), authFrom(c), c.Param("subscriptionId"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// Resume godoc
// @Summary      Возобновить suspended-подписку
// @Tags         billing
// @Produce      json
// @Param        subscriptionId path string true "UUID"
// @Success      200 {object} in.ResumeResult
// @Security     BearerAuth
// @Router       /billing/subscriptions/{subscriptionId}/resume [post]
func (h *BillingHandler) Resume(c *gin.Context) {
	if !validUUID(c.Param("subscriptionId")) {
		respondError(c, usecase.NewError(usecase.CodeNotFound, "subscription not found"))
		return
	}
	result, err := h.service.Resume(c.Request.Context(), authFrom(c), c.Param("subscriptionId"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// ListPayments godoc
// @Summary      История платежей
// @Tags         billing
// @Produce      json
// @Param        subscriptionId path string true "UUID"
// @Success      200 {array} in.PaymentView
// @Security     BearerAuth
// @Router       /billing/subscriptions/{subscriptionId}/payments [get]
func (h *BillingHandler) ListPayments(c *gin.Context) {
	if !validUUID(c.Param("subscriptionId")) {
		respondError(c, usecase.NewError(usecase.CodeNotFound, "subscription not found"))
		return
	}
	payments, err := h.service.ListPayments(c.Request.Context(), authFrom(c), c.Param("subscriptionId"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, payments)
}

// AcquiringWebhook godoc
// @Summary      Нотификатор WebPay
// @Description  HMAC → timestamp 5 мин → idempotency по transaction_id.
// @Tags         webhooks
// @Accept       x-www-form-urlencoded
// @Success      200 {string} string "OK"
// @Failure      401,404 {object} ErrorResponse
// @Router       /billing/webhooks/acquiring [post]
func (h *BillingHandler) AcquiringWebhook(c *gin.Context) {
	if err := c.Request.ParseForm(); err != nil {
		respondError(c, usecase.NewError(usecase.CodeInvalidInput, "invalid form body"))
		return
	}
	form := c.Request.PostForm
	batchTimestamp, _ := strconv.ParseInt(form.Get("batch_timestamp"), 10, 64)
	paymentType, _ := strconv.Atoi(form.Get("payment_type"))
	action, _ := strconv.Atoi(form.Get("action"))

	err := h.service.HandleWebhook(c.Request.Context(), in.WebhookInput{
		BatchTimestamp: batchTimestamp,
		CurrencyID:     form.Get("currency_id"),
		Amount:         form.Get("amount"),
		PaymentMethod:  form.Get("payment_method"),
		OrderID:        form.Get("order_id"),
		SiteOrderID:    form.Get("site_order_id"),
		TransactionID:  form.Get("transaction_id"),
		PaymentType:    paymentType,
		RRN:            form.Get("rrn"),
		Card:           form.Get("card"),
		Signature:      firstNonEmpty(form.Get("wsb_signature"), form.Get("signature")),
		Action:         action,
		RC:             form.Get("rc"),
		CustomerID:     form.Get("customer_id"),
		OperationType:  form.Get("operation_type"),
		RecurringToken: form.Get("recurring_token"),
		OfferExpDate:   form.Get("offer_exp_date"),
	})
	if err != nil {
		var appErr *usecase.Error
		if errors.As(err, &appErr) && appErr.Code == usecase.CodeForbidden {
			c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "unauthorized", Message: appErr.Message})
			return
		}
		respondError(c, err)
		return
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte("OK"))
}

type patchRequest struct {
	ScheduledChange *struct {
		TargetTariffPlanID string     `json:"targetTariffPlanId"`
		EffectiveAt        *time.Time `json:"effectiveAt"`
	} `json:"scheduledChange"`
}

// PatchSubscription godoc
// @Summary      Admin: запланировать смену тарифа
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        subscriptionId path string true "UUID"
// @Param        request body patchRequest true "scheduledChange"
// @Success      200 {object} in.SubscriptionView
// @Security     BearerAuth
// @Router       /billing/subscriptions/{subscriptionId} [patch]
func (h *BillingHandler) PatchSubscription(c *gin.Context) {
	var request patchRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		respondError(c, usecase.NewError(usecase.CodeInvalidInput, "invalid JSON body"))
		return
	}
	if !validUUID(c.Param("subscriptionId")) {
		respondError(c, usecase.NewError(usecase.CodeNotFound, "subscription not found"))
		return
	}
	input := in.PatchInput{SubscriptionID: c.Param("subscriptionId")}
	if request.ScheduledChange == nil {
		respondError(c, usecase.NewError(usecase.CodeInvalidInput, "scheduledChange is required"))
		return
	}
	{
		if !validUUID(request.ScheduledChange.TargetTariffPlanID) {
			respondError(c, usecase.NewError(usecase.CodeInvalidInput, "targetTariffPlanId is required"))
			return
		}
		input.TargetTariffPlanID = request.ScheduledChange.TargetTariffPlanID
		input.EffectiveAt = request.ScheduledChange.EffectiveAt
	}
	view, err := h.service.PatchSubscription(c.Request.Context(), authFrom(c), input)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

type adminActivateRequest struct {
	TariffPlanID string     `json:"tariffPlanId"`
	PeriodStart  *time.Time `json:"periodStart"`
	PeriodEnd    *time.Time `json:"periodEnd"`
}

// AdminActivateTariff godoc
// @Summary      Admin: активировать тариф аккаунту
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        accountId path string true "UUID"
// @Param        Idempotency-Key header string true "UUID"
// @Param        request body adminActivateRequest true "tariffPlanId"
// @Success      201 {object} in.AdminActivateResult
// @Security     BearerAuth
// @Router       /admin/billing/accounts/{accountId}/activate-tariff [post]
func (h *BillingHandler) AdminActivateTariff(c *gin.Context) {
	var request adminActivateRequest
	if err := c.ShouldBindJSON(&request); err != nil || !validUUID(request.TariffPlanID) {
		respondError(c, usecase.NewError(usecase.CodeInvalidInput, "tariffPlanId is required"))
		return
	}
	if !validUUID(c.Param("accountId")) {
		respondError(c, usecase.NewError(usecase.CodeInvalidInput, "accountId must be a UUID"))
		return
	}
	result, err := h.service.AdminActivateTariff(c.Request.Context(), authFrom(c), in.AdminActivateInput{
		AccountID:    c.Param("accountId"),
		TariffPlanID: request.TariffPlanID,
		PeriodStart:  request.PeriodStart,
		PeriodEnd:    request.PeriodEnd,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

// AdminDeactivateTariff godoc
// @Summary      Admin: деактивировать тариф аккаунта
// @Tags         admin
// @Produce      json
// @Param        accountId path string true "UUID"
// @Param        Idempotency-Key header string true "UUID"
// @Success      200 {object} in.AdminDeactivateResult
// @Security     BearerAuth
// @Router       /admin/billing/accounts/{accountId}/deactivate-tariff [post]
func (h *BillingHandler) AdminDeactivateTariff(c *gin.Context) {
	if !validUUID(c.Param("accountId")) {
		respondError(c, usecase.NewError(usecase.CodeInvalidInput, "accountId must be a UUID"))
		return
	}
	result, err := h.service.AdminDeactivateTariff(c.Request.Context(), authFrom(c), c.Param("accountId"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

func respondError(c *gin.Context, err error) {
	var quoteExpired *in.QuoteExpiredError
	if errors.As(err, &quoteExpired) {
		c.JSON(http.StatusConflict, gin.H{"error": "quote_expired", "refreshedPreview": quoteExpired.Refreshed})
		return
	}
	var appErr *usecase.Error
	if errors.As(err, &appErr) {
		status := http.StatusInternalServerError
		switch appErr.Code {
		case usecase.CodeInvalidInput, usecase.CodeNotSuspended, usecase.CodePeriodExpired, usecase.CodePeriodEnded, usecase.CodeNotUpgrade:
			status = http.StatusBadRequest
		case usecase.CodeNotFound:
			status = http.StatusNotFound
		case usecase.CodeForbidden, usecase.CodeAccountInactive:
			status = http.StatusForbidden
		case usecase.CodeActiveSubscription, usecase.CodeIdempotencyInProgress, usecase.CodeConflict:
			status = http.StatusConflict
		case usecase.CodeRateLimit:
			status = http.StatusTooManyRequests
		case usecase.CodePaymentFailed:
			status = http.StatusBadRequest
		}
		c.JSON(status, ErrorResponse{Error: appErr.Code, Message: appErr.Message})
		return
	}
	c.JSON(http.StatusInternalServerError, ErrorResponse{Error: usecase.CodeInternal, Message: "internal error"})
	slog.Error("billing handler internal error", slog.String("error", err.Error()))
}

func validUUID(raw string) bool {
	_, err := uuid.Parse(raw)
	return err == nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
