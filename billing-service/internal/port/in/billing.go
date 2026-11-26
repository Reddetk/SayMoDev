// Package in — первичные порты BC#2 и DTO на границе (ADR-007).
package in

// goca:manual — port-owned DTO (ADR-007), goca генерирует handler без портов.

import (
	"context"
	"time"
)

// AuthContext — результат валидации JWT, передаётся в бизнес-логику.
type AuthContext struct {
	AccountID string
	Role      string
	SessionID string
	Rev       int
}

// TariffSnapshotView — денормализованные правила тарифа.
type TariffSnapshotView struct {
	WeekBitmap int16  `json:"weekBitmap"`
	Timezone   string `json:"timezone"`
}

// PeriodView — расчётный период.
type PeriodView struct {
	PeriodStart time.Time `json:"periodStart"`
	PeriodEnd   time.Time `json:"periodEnd"`
}

// ScheduledChangeView — отложенный downgrade.
type ScheduledChangeView struct {
	TargetTariffPlanID string    `json:"targetTariffPlanId"`
	EffectiveAt        time.Time `json:"effectiveAt"`
}

// SubscriptionView — GET /billing/subscriptions/{id}.
type SubscriptionView struct {
	SubscriptionID  string               `json:"subscriptionId"`
	AccountID       string               `json:"accountId"`
	Status          string               `json:"status"`
	TariffSnapshot  TariffSnapshotView   `json:"tariffSnapshot"`
	BillingPeriod   PeriodView           `json:"billingPeriod"`
	ScheduledChange *ScheduledChangeView `json:"scheduledChange"`
	RevokedAt       *time.Time           `json:"revoked_at"`
}

// PaymentView — элемент истории платежей.
type PaymentView struct {
	PaymentID           string    `json:"paymentId"`
	AmountKopecks       int64     `json:"amount_kopecks"`
	Currency            string    `json:"currency"`
	Status              string    `json:"status"`
	TariffPlanName      string    `json:"tariffPlanName"`
	PaymentKind         string    `json:"paymentKind"`
	ExternalReferenceID string    `json:"externalReferenceId"`
	CreatedAt           time.Time `json:"createdAt"`
}

// InitiatedPayment — созданный pending-платёж и ссылка на WebPay.
type InitiatedPayment struct {
	PaymentID            string `json:"paymentId"`
	AmountKopecks        int64  `json:"amount_kopecks"`
	Status               string `json:"status"`
	AcquiringRedirectURL string `json:"acquiringRedirectUrl"`
}

// SubscribeInput — POST /billing/subscriptions.
type SubscribeInput struct {
	AccountID      string
	TariffPlanID   string
	PaymentType    string
	IdempotencyKey string
}

// SubscribeResult — 201 ответ.
type SubscribeResult struct {
	SubscriptionID string             `json:"subscriptionId"`
	Status         string             `json:"status"`
	TariffSnapshot TariffSnapshotView `json:"tariffSnapshot"`
	BillingPeriod  PeriodView         `json:"billingPeriod"`
	Payment        InitiatedPayment   `json:"payment"`
}

// ProrationView — расчёт доплаты.
type ProrationView struct {
	AmountKopecks int64  `json:"amount_kopecks"`
	Currency      string `json:"currency"`
	RemainingDays int    `json:"remaining_days"`
	NewDailyRate  int64  `json:"new_daily_rate_kopecks"`
	OldDailyRate  int64  `json:"old_daily_rate_kopecks"`
}

// UpgradePreview — GET .../upgrade/preview.
type UpgradePreview struct {
	CurrentTariffPlanID string        `json:"currentTariffPlanId"`
	TargetTariffPlanID  string        `json:"targetTariffPlanId"`
	Proration           ProrationView `json:"proration"`
	QuoteID             string        `json:"quoteId"`
	QuoteExpiresAt      time.Time     `json:"quoteExpiresAt"`
}

// QuoteExpiredError — 409 с обновлённым preview.
type QuoteExpiredError struct {
	Refreshed UpgradePreview
}

func (e *QuoteExpiredError) Error() string { return "quote_expired" }

// UpgradeInput — POST .../upgrade.
type UpgradeInput struct {
	AccountID      string
	SubscriptionID string
	TargetTariff   string
	QuoteID        string
}

// UpgradeResult — 200 ответ upgrade.
type UpgradeResult struct {
	SubscriptionID       string             `json:"subscriptionId"`
	Status               string             `json:"status"`
	TariffSnapshot       TariffSnapshotView `json:"tariffSnapshot"`
	ProrationCharge      ProrationCharge    `json:"prorationCharge"`
	AcquiringRedirectURL string             `json:"acquiringRedirectUrl,omitempty"`
}

// ProrationCharge — списанная доплата.
type ProrationCharge struct {
	AmountKopecks int64  `json:"amount_kopecks"`
	Currency      string `json:"currency"`
}

// DowngradeInput — POST .../downgrade.
type DowngradeInput struct {
	AccountID      string
	SubscriptionID string
	TargetTariff   string
}

// DowngradeResult — 200 ответ downgrade.
type DowngradeResult struct {
	SubscriptionID  string               `json:"subscriptionId"`
	Status          string               `json:"status"`
	ScheduledChange *ScheduledChangeView `json:"scheduledChange"`
	Message         string               `json:"message"`
}

// CancelResult — 200 ответ cancel.
type CancelResult struct {
	SubscriptionID string     `json:"subscriptionId"`
	Status         string     `json:"status"`
	RevokedAt      *time.Time `json:"revoked_at"`
	Message        string     `json:"message"`
}

// ResumeResult — 200 ответ resume.
type ResumeResult struct {
	SubscriptionID string     `json:"subscriptionId"`
	Status         string     `json:"status"`
	RevokedAt      *time.Time `json:"revoked_at"`
	Message        string     `json:"message"`
}

// WebhookInput — разобранный нотификатор WebPay.
type WebhookInput struct {
	BatchTimestamp int64
	CurrencyID     string
	Amount         string
	PaymentMethod  string
	OrderID        string
	SiteOrderID    string
	TransactionID  string
	PaymentType    int
	RRN            string
	Card           string
	Signature      string
	Action         int
	RC             string
	CustomerID     string
	OperationType  string
	RecurringToken string
	OfferExpDate   string
}

// PatchInput — PATCH /billing/subscriptions/{id} (admin).
type PatchInput struct {
	SubscriptionID     string
	TargetTariffPlanID string
	EffectiveAt        *time.Time
}

// AdminActivateInput — POST /admin/billing/accounts/{id}/activate-tariff.
type AdminActivateInput struct {
	AccountID    string
	TariffPlanID string
	PeriodStart  *time.Time
	PeriodEnd    *time.Time
}

// AdminActivateResult — 201 ответ.
type AdminActivateResult struct {
	SubscriptionID string             `json:"subscriptionId"`
	Status         string             `json:"status"`
	TariffSnapshot TariffSnapshotView `json:"tariffSnapshot"`
}

// AdminDeactivateResult — 200 ответ.
type AdminDeactivateResult struct {
	SubscriptionID string     `json:"subscriptionId"`
	Status         string     `json:"status"`
	RevokedAt      *time.Time `json:"revoked_at"`
}

// BillingService — первичный порт BC#2.
type BillingService interface {
	Subscribe(ctx context.Context, auth AuthContext, input SubscribeInput) (SubscribeResult, error)
	GetSubscription(ctx context.Context, auth AuthContext, subscriptionID string) (SubscriptionView, error)
	PreviewUpgrade(ctx context.Context, auth AuthContext, subscriptionID, targetTariffPlanID string) (UpgradePreview, error)
	Upgrade(ctx context.Context, auth AuthContext, input UpgradeInput) (UpgradeResult, error)
	Downgrade(ctx context.Context, auth AuthContext, input DowngradeInput) (DowngradeResult, error)
	Cancel(ctx context.Context, auth AuthContext, subscriptionID string) (CancelResult, error)
	Resume(ctx context.Context, auth AuthContext, subscriptionID string) (ResumeResult, error)
	ListPayments(ctx context.Context, auth AuthContext, subscriptionID string) ([]PaymentView, error)
	HandleWebhook(ctx context.Context, input WebhookInput) error
	PatchSubscription(ctx context.Context, auth AuthContext, input PatchInput) (SubscriptionView, error)
	AdminActivateTariff(ctx context.Context, auth AuthContext, input AdminActivateInput) (AdminActivateResult, error)
	AdminDeactivateTariff(ctx context.Context, auth AuthContext, accountID string) (AdminDeactivateResult, error)
}
