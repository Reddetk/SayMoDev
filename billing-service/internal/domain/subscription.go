package domain

import (
	"fmt"
	"time"
)

// SubscriptionStatus — жизненный цикл подписки (§1–§8).
type SubscriptionStatus string

const (
	StatusPending   SubscriptionStatus = "pending"
	StatusActive    SubscriptionStatus = "active"
	StatusCancelled SubscriptionStatus = "cancelled"
	StatusExpired   SubscriptionStatus = "expired"
	StatusSuspended SubscriptionStatus = "suspended"
)

// Valid проверяет статус.
func (s SubscriptionStatus) Valid() bool {
	switch s {
	case StatusPending, StatusActive, StatusCancelled, StatusExpired, StatusSuspended:
		return true
	default:
		return false
	}
}

// Subscription — агрегат BC#2.
type Subscription struct {
	ID           UUID
	AccountID    UUID
	TariffPlanID UUID
	OfferID      UUID
	Status       SubscriptionStatus
	Snapshot     TariffSnapshot
	Period       BillingPeriod
	Scheduled    *ScheduledChange
	RevokedAt    *time.Time
	RetryCount   int
	NextRetryAt  *time.Time

	Payments      []Payment
	PaymentMethod *PaymentMethod

	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewSubscription создаёт подписку в статусе pending (§5).
func NewSubscription(
	id, accountID, tariffPlanID, offerID UUID,
	snapshot TariffSnapshot,
	period BillingPeriod,
	now time.Time,
) (*Subscription, error) {
	if id == (UUID{}) || accountID == (UUID{}) || tariffPlanID == (UUID{}) || offerID == (UUID{}) {
		return nil, fmt.Errorf("%w: subscription ids are required", ErrInvalidArgument)
	}
	if _, err := NewTariffSnapshot(snapshot.WeekBitmap, snapshot.Timezone); err != nil {
		return nil, err
	}
	return &Subscription{
		ID:           id,
		AccountID:    accountID,
		TariffPlanID: tariffPlanID,
		OfferID:      offerID,
		Status:       StatusPending,
		Snapshot:     snapshot,
		Period:       period,
		CreatedAt:    now.UTC(),
		UpdatedAt:    now.UTC(),
	}, nil
}

// Activate pending → active только при успешном платеже (§5, §6).
func (s *Subscription) Activate(paymentSuccess bool, now time.Time) error {
	if s.Status != StatusPending {
		return fmt.Errorf("%w: %s -> active", ErrInvalidTransition, s.Status)
	}
	if !paymentSuccess {
		return ErrPaymentRequired
	}
	s.Status = StatusActive
	s.UpdatedAt = now.UTC()
	return nil
}

// Suspend pending|active → suspended (§7).
func (s *Subscription) Suspend(now time.Time) error {
	if s.Status != StatusPending && s.Status != StatusActive {
		return fmt.Errorf("%w: %s -> suspended", ErrInvalidTransition, s.Status)
	}
	s.Status = StatusSuspended
	s.UpdatedAt = now.UTC()
	return nil
}

// Resume suspended → active, если период не истёк (§7).
func (s *Subscription) Resume(now time.Time) error {
	if s.Status != StatusSuspended {
		return fmt.Errorf("%w: %s -> resume", ErrInvalidTransition, s.Status)
	}
	if s.Period.Expired(now) {
		return ErrPeriodExpired
	}
	s.Status = StatusActive
	s.RevokedAt = nil
	s.UpdatedAt = now.UTC()
	return nil
}

// Cancel pending|active → cancelled; доступ живёт до конца периода (§2, §8).
func (s *Subscription) Cancel(now time.Time) error {
	if s.Status != StatusPending && s.Status != StatusActive {
		return fmt.Errorf("%w: %s -> cancelled", ErrInvalidTransition, s.Status)
	}
	now = now.UTC()
	s.Status = StatusCancelled
	s.RevokedAt = &now
	s.UpdatedAt = now
	return nil
}

// Expire active|suspended|cancelled → expired (§7, §8).
func (s *Subscription) Expire(now time.Time) error {
	switch s.Status {
	case StatusActive, StatusSuspended, StatusCancelled:
	default:
		return fmt.Errorf("%w: %s -> expired", ErrInvalidTransition, s.Status)
	}
	now = now.UTC()
	s.Status = StatusExpired
	s.RevokedAt = &now
	s.Scheduled = nil
	s.NextRetryAt = nil
	s.UpdatedAt = now
	return nil
}

// ApplyUpgrade active → active немедленно, период не меняется (§3).
func (s *Subscription) ApplyUpgrade(targetPlanID, targetOfferID UUID, snapshot TariffSnapshot, now time.Time) error {
	if s.Status != StatusActive {
		return fmt.Errorf("%w: %s -> upgrade", ErrInvalidTransition, s.Status)
	}
	if s.Period.Expired(now) {
		return ErrPeriodExpired
	}
	if targetPlanID == (UUID{}) || targetOfferID == (UUID{}) {
		return fmt.Errorf("%w: target ids are required", ErrInvalidArgument)
	}
	s.TariffPlanID = targetPlanID
	s.OfferID = targetOfferID
	s.Snapshot = snapshot
	s.Scheduled = nil
	s.UpdatedAt = now.UTC()
	return nil
}

// ScheduleDowngrade active → active + ScheduledChange на следующий период (§3).
func (s *Subscription) ScheduleDowngrade(targetPlanID UUID, effectiveAt, now time.Time) error {
	if s.Status != StatusActive {
		return fmt.Errorf("%w: %s -> downgrade", ErrInvalidTransition, s.Status)
	}
	if targetPlanID == (UUID{}) {
		return fmt.Errorf("%w: target tariff id is required", ErrInvalidArgument)
	}
	if !effectiveAt.After(now) {
		return fmt.Errorf("%w: effective_at must be in the future", ErrInvalidArgument)
	}
	s.Scheduled = &ScheduledChange{TargetTariffPlanID: targetPlanID, EffectiveAt: effectiveAt.UTC()}
	s.UpdatedAt = now.UTC()
	return nil
}

// ApplyScheduledChange применяет downgrade при renewal.
func (s *Subscription) ApplyScheduledChange(offerID UUID, planID UUID, snapshot TariffSnapshot, now time.Time) error {
	if s.Scheduled == nil {
		return fmt.Errorf("%w: no scheduled change", ErrInvalidArgument)
	}
	if s.Status != StatusActive {
		return fmt.Errorf("%w: %s -> apply scheduled change", ErrInvalidTransition, s.Status)
	}
	if offerID == (UUID{}) {
		return fmt.Errorf("%w: offer id is required", ErrInvalidArgument)
	}
	s.OfferID = offerID
	s.TariffPlanID = planID
	s.Snapshot = snapshot
	s.Scheduled = nil
	s.UpdatedAt = now.UTC()
	return nil
}

// Renew продлевает период (вызывается вместе с созданием renewal-платежа).
func (s *Subscription) Renew(period BillingPeriod, now time.Time) error {
	if s.Status != StatusActive {
		return fmt.Errorf("%w: %s -> renew", ErrInvalidTransition, s.Status)
	}
	s.Period = period
	s.RetryCount = 0
	s.NextRetryAt = nil
	s.UpdatedAt = now.UTC()
	return nil
}

// AddPayment добавляет платёж в агрегат.
func (s *Subscription) AddPayment(payment Payment) error {
	if payment.ID == (UUID{}) {
		return fmt.Errorf("%w: payment id is required", ErrInvalidArgument)
	}
	for _, existing := range s.Payments {
		if existing.ID == payment.ID {
			return nil
		}
	}
	s.Payments = append(s.Payments, payment)
	return nil
}

// SetPaymentMethod сохраняет привязку карты; активным может быть только один метод (§6).
func (s *Subscription) SetPaymentMethod(method PaymentMethod, now time.Time) error {
	if s.PaymentMethod != nil && s.PaymentMethod.IsActive && s.PaymentMethod.ID != method.ID {
		s.PaymentMethod.Unbind(now)
	}
	copy := method
	s.PaymentMethod = &copy
	return nil
}

// RegisterPaymentFailure увеличивает счётчик retry и назначает следующую попытку.
func (s *Subscription) RegisterPaymentFailure(nextRetryAt *time.Time, now time.Time) {
	s.RetryCount++
	s.NextRetryAt = nextRetryAt
	s.UpdatedAt = now.UTC()
}

// FindPayment возвращает платёж по ID.
func (s *Subscription) FindPayment(id UUID) (*Payment, bool) {
	for i := range s.Payments {
		if s.Payments[i].ID == id {
			return &s.Payments[i], true
		}
	}
	return nil, false
}

// Abandon закрывает брошенную pending-подписку (нет оплаты > 24 ч).
// Отдельный метод: публичный переход pending→expired не разрешён событиями §8.
func (s *Subscription) Abandon(now time.Time) error {
	if s.Status != StatusPending {
		return fmt.Errorf("%w: %s -> abandon", ErrInvalidTransition, s.Status)
	}
	now = now.UTC()
	s.Status = StatusExpired
	s.RevokedAt = &now
	s.UpdatedAt = now
	return nil
}

// Validate проверяет агрегатные инварианты перед сохранением.
func (s *Subscription) Validate() error {
	if s.ID == (UUID{}) || s.AccountID == (UUID{}) {
		return fmt.Errorf("%w: subscription ids are required", ErrInvalidArgument)
	}
	if !s.Status.Valid() {
		return fmt.Errorf("%w: status %q", ErrInvalidArgument, s.Status)
	}
	if _, err := NewBillingPeriod(s.Period.Start, s.Period.End); err != nil {
		return err
	}
	if _, err := NewTariffSnapshot(s.Snapshot.WeekBitmap, s.Snapshot.Timezone); err != nil {
		return err
	}
	for _, payment := range s.Payments {
		if _, err := NewPaymentAmount(payment.Amount.AmountKopecks, payment.Amount.Currency); err != nil {
			return err
		}
	}
	return nil
}
