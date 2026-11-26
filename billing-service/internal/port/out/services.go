package out

import (
	"context"
	"time"
)

// QuoteSnapshot — зафиксированный расчёт upgrade (Redis TTL 5 мин).
type QuoteSnapshot struct {
	QuoteID             string
	AccountID           string
	SubscriptionID      string
	CurrentTariffPlanID string
	TargetTariffPlanID  string
	TargetOfferID       string
	OldPriceKopecks     int64
	NewPriceKopecks     int64
	ChargeKopecks       int64
	Currency            string
	BillingDays         int
	RemainingDays       int
	ExpiresAt           time.Time
}

// Cache — Redis L2 BC#2: idempotency, quotes, rate limiting, revocation.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// AllowUpgradeChange реализует §12: ≤3 смен / 24 ч, ≥1 ч между сменами.
	AllowUpgradeChange(ctx context.Context, accountID string, now time.Time) (bool, error)
	// IsRevoked проверяет jti blacklist и account:rev (BC#1 shared Redis).
	IsRevoked(ctx context.Context, jti, accountID string, tokenRev int) (bool, error)
}

// PaymentLinkRequest — параметры redirect-based платежа WebPay.
type PaymentLinkRequest struct {
	StoreID       string
	CustomerID    string
	OrderNum      string
	AmountKopecks int64
	Currency      string
	NotifyURL     string
	ReturnURL     string
	OperationType string
	Description   string
}

// RecurringPayRequest — recurring_pay для renewal.
type RecurringPayRequest struct {
	OrderNum       string
	AmountKopecks  int64
	Currency       string
	RecurringToken string
	CustomerID     string
}

// UnbindRequest — recurring_unbind.
type UnbindRequest struct {
	RecurringToken string
	CustomerID     string
}

// Acquiring — ACL-порт внешнего эквайринга (WebPay).
type Acquiring interface {
	CreatePaymentLink(ctx context.Context, req PaymentLinkRequest) (string, error)
	RecurringPay(ctx context.Context, req RecurringPayRequest) error
	Unbind(ctx context.Context, req UnbindRequest) error
}

// Notification — письмо через Postbox ACL.
type Notification struct {
	Kind           string
	Recipient      string
	AccountID      string
	SubscriptionID string
	PeriodEnd      time.Time
	CardExpDate    time.Time
	RetryPossible  bool
}

// Notifier — ACL-порт Postbox.
type Notifier interface {
	Send(ctx context.Context, notification Notification) error
}
