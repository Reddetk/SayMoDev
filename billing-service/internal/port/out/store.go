// Package out — вторичные порты BC#2.
//
// DTO принадлежат пакету порта (ADR-007): доменные типы не пересекают границу.
package out

// goca:manual — port-owned DTO (ADR-007), goca генерирует repository без портов.

import (
	"context"
	"time"
)

// SubscriptionRecord — снимок агрегата для persistence.
type SubscriptionRecord struct {
	ID              string
	AccountID       string
	TariffPlanID    string
	OfferID         string
	Status          string
	WeekBitmap      int16
	Timezone        string
	PeriodStart     time.Time
	PeriodEnd       time.Time
	ScheduledTarget *string
	ScheduledAt     *time.Time
	RevokedAt       *time.Time
	RetryCount      int
	NextRetryAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// PaymentRecord — платёж.
type PaymentRecord struct {
	ID                  string
	SubscriptionID      string
	AmountKopecks       int64
	Currency            string
	Status              string
	Kind                string
	TariffPlanName      string
	ExternalReferenceID string
	AppliedOfferID      *string
	RevertTariffPlanID  *string
	RevertOfferID       *string
	RevertWeekBitmap    *int16
	RevertTimezone      *string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// PaymentMethodRecord — привязанная карта.
type PaymentMethodRecord struct {
	ID             string
	SubscriptionID string
	Type           string
	WSBCustomerID  string
	RecurringToken string
	CardMask       string
	CardExpDate    *time.Time
	IsActive       bool
	BoundAt        time.Time
}

// OfferRecord — каталог: оффер вместе с тарифным планом.
type OfferRecord struct {
	OfferID      string
	TariffPlanID string
	PlanName     string
	WeekBitmap   int16
	Timezone     string
	PriceKopecks int64
	Currency     string
	BillingDays  int
	ValidFrom    time.Time
	ValidUntil   *time.Time
	IsActive     bool
}

// ExpiringMethod — карта, истекающая в горизонте уведомления.
type ExpiringMethod struct {
	SubscriptionID string
	AccountID      string
	CardExpDate    time.Time
}

// IdempotencyRecord — сохранённый результат команды.
type IdempotencyRecord struct {
	Key          string
	AccountID    string
	Endpoint     string
	Status       string
	ResponseCode int
	ResponseBody []byte
	ExpiresAt    time.Time
}

// OutboxRecord — доменное событие для Kafka.
type OutboxRecord struct {
	ID         int64
	EventID    string
	EventType  string
	Payload    []byte
	OccurredAt time.Time
}

// Store — все операции persistence BC#2. Реализация — pgx.
type Store interface {
	// WithinTx выполняет fn в транзакции; при ошибке — rollback.
	WithinTx(ctx context.Context, fn func(Store) error) error

	CreateSubscription(ctx context.Context, rec SubscriptionRecord) error
	GetSubscription(ctx context.Context, id string) (*SubscriptionRecord, error)
	GetSubscriptionForUpdate(ctx context.Context, id string) (*SubscriptionRecord, error)
	GetSubscriptionByAccount(ctx context.Context, accountID string) (*SubscriptionRecord, error)
	GetLatestSubscriptionByAccount(ctx context.Context, accountID string) (*SubscriptionRecord, error)
	UpdateSubscription(ctx context.Context, rec SubscriptionRecord) error
	ListSubscriptionsDue(ctx context.Context, status string, now time.Time, limit int) ([]SubscriptionRecord, error)
	ListSubscriptionsEndingBefore(ctx context.Context, status string, before time.Time, limit int) ([]SubscriptionRecord, error)
	ListStalePendingSubscriptions(ctx context.Context, before time.Time, limit int) ([]SubscriptionRecord, error)
	ListStaleRenewalPayments(ctx context.Context, before time.Time, limit int) ([]PaymentRecord, error)
	ListStaleProrationPayments(ctx context.Context, before time.Time, limit int) ([]PaymentRecord, error)
	Now(ctx context.Context) (time.Time, error)

	CreatePayment(ctx context.Context, rec PaymentRecord) error
	UpdatePayment(ctx context.Context, rec PaymentRecord) error
	GetPayment(ctx context.Context, id string) (*PaymentRecord, error)
	GetPaymentForUpdate(ctx context.Context, id string) (*PaymentRecord, error)
	GetPaymentByExternalRef(ctx context.Context, ref string) (*PaymentRecord, error)
	ListPayments(ctx context.Context, subscriptionID string) ([]PaymentRecord, error)

	UpsertPaymentMethod(ctx context.Context, rec PaymentMethodRecord) error
	GetActivePaymentMethod(ctx context.Context, subscriptionID string) (*PaymentMethodRecord, error)
	DeactivatePaymentMethods(ctx context.Context, subscriptionID string) error
	ListExpiringPaymentMethods(ctx context.Context, before time.Time) ([]ExpiringMethod, error)

	GetOffer(ctx context.Context, offerID string) (*OfferRecord, error)
	FindActiveOffer(ctx context.Context, tariffPlanID string, at time.Time) (*OfferRecord, error)

	GetIdempotency(ctx context.Context, accountID, key string) (*IdempotencyRecord, error)
	CreateIdempotency(ctx context.Context, rec IdempotencyRecord) (bool, error)
	CompleteIdempotency(ctx context.Context, rec IdempotencyRecord) error
	DeleteIdempotency(ctx context.Context, accountID, key string) error

	InsertOutbox(ctx context.Context, eventID, eventType string, payload []byte) error
	FetchOutboxBatch(ctx context.Context, limit int) ([]OutboxRecord, error)
	MarkOutboxSent(ctx context.Context, id int64) error
	MarkOutboxFailed(ctx context.Context, id int64, reason string) error

	ProcessedEventExists(ctx context.Context, eventID, consumer string) (bool, error)
	MarkProcessedEvent(ctx context.Context, eventID, consumer string) error

	GetAccountStatus(ctx context.Context, accountID string) (string, error)
	UpsertAccountStatus(ctx context.Context, accountID, status string) error

	ArchiveAccount(ctx context.Context, accountID string, snapshot []byte) error
	DeleteSubscription(ctx context.Context, id string) error

	SubscriptionCounts(ctx context.Context) ([]SubscriptionCount, error)
	PendingOutboxCount(ctx context.Context) (int64, error)
}

// SubscriptionCount — агрегат для gauge active_subscriptions_count.
type SubscriptionCount struct {
	Status   string
	PlanType string
	Count    int64
}

// ErrNotFound — запись отсутствует (маппится в 404, не 403: IDOR prevention).
var ErrNotFound = errNotFound{}

type errNotFound struct{}

func (errNotFound) Error() string { return "not found" }
