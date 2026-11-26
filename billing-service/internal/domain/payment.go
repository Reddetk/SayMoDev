package domain

import (
	"fmt"
	"time"
)

// PaymentStatus — финальные статусы платежа (§4, §6).
type PaymentStatus string

const (
	PaymentPending  PaymentStatus = "pending"
	PaymentSuccess  PaymentStatus = "success"
	PaymentFailed   PaymentStatus = "failed"
	PaymentRefunded PaymentStatus = "refunded"
)

// Final сообщает, является ли статус финальным.
func (s PaymentStatus) Final() bool { return s != PaymentPending }

// PaymentKind — тип платежа для истории.
type PaymentKind string

const (
	PaymentInitial   PaymentKind = "initial"
	PaymentRenewal   PaymentKind = "renewal"
	PaymentProration PaymentKind = "upgrade_proration"
)

// Valid проверяет допустимый kind.
func (k PaymentKind) Valid() bool {
	switch k {
	case PaymentInitial, PaymentRenewal, PaymentProration:
		return true
	default:
		return false
	}
}

// Payment — entity внутри агрегата Subscription.
type Payment struct {
	ID                  UUID
	Amount              Money
	Status              PaymentStatus
	Kind                PaymentKind
	TariffPlanName      string
	ExternalReferenceID string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// NewPayment создаёт платёж в статусе pending (создаётся ДО вызова acquiring, §6).
func NewPayment(id UUID, amount Money, kind PaymentKind, tariffPlanName string, now time.Time) (Payment, error) {
	if id == (UUID{}) {
		return Payment{}, fmt.Errorf("%w: payment id is required", ErrInvalidArgument)
	}
	if !kind.Valid() {
		return Payment{}, fmt.Errorf("%w: payment kind %q", ErrInvalidArgument, kind)
	}
	if _, err := NewPaymentAmount(amount.AmountKopecks, amount.Currency); err != nil {
		return Payment{}, err
	}
	return Payment{
		ID:             id,
		Amount:         amount,
		Status:         PaymentPending,
		Kind:           kind,
		TariffPlanName: tariffPlanName,
		CreatedAt:      now.UTC(),
		UpdatedAt:      now.UTC(),
	}, nil
}

// MarkSuccess переводит pending → success. Повторный success идемпотентен.
func (p *Payment) MarkSuccess(externalReferenceID string, now time.Time) error {
	if p.Status == PaymentSuccess {
		return nil
	}
	if p.Status != PaymentPending {
		return fmt.Errorf("%w: payment %s -> success", ErrInvalidTransition, p.Status)
	}
	if externalReferenceID == "" {
		return fmt.Errorf("%w: transaction_id is required", ErrInvalidArgument)
	}
	p.Status = PaymentSuccess
	p.ExternalReferenceID = externalReferenceID
	p.UpdatedAt = now.UTC()
	return nil
}

// MarkFailed переводит pending → failed (финальный статус, §4).
func (p *Payment) MarkFailed(now time.Time) error {
	if p.Status == PaymentFailed {
		return nil
	}
	if p.Status != PaymentPending {
		return fmt.Errorf("%w: payment %s -> failed", ErrInvalidTransition, p.Status)
	}
	p.Status = PaymentFailed
	p.UpdatedAt = now.UTC()
	return nil
}

// PaymentMethod — entity внутри Subscription, максимум один активный (§6).
type PaymentMethod struct {
	ID             UUID
	Type           string
	WSBCustomerID  string
	RecurringToken string
	CardMask       string
	CardExpDate    time.Time
	IsActive       bool
	BoundAt        time.Time
}

// NewPaymentMethod создаёт привязанную карту (operation_type=recurring_bind).
func NewPaymentMethod(id UUID, customerID, token, cardMask string, cardExpDate time.Time, now time.Time) (PaymentMethod, error) {
	if id == (UUID{}) || customerID == "" {
		return PaymentMethod{}, fmt.Errorf("%w: payment method ids are required", ErrInvalidArgument)
	}
	if token == "" {
		return PaymentMethod{}, fmt.Errorf("%w: recurring_token is required", ErrInvalidArgument)
	}
	return PaymentMethod{
		ID:             id,
		Type:           "card",
		WSBCustomerID:  customerID,
		RecurringToken: token,
		CardMask:       cardMask,
		CardExpDate:    cardExpDate.UTC(),
		IsActive:       true,
		BoundAt:        now.UTC(),
	}, nil
}

// Unbind деактивирует метод (recurring_unbind, ADR-005).
func (m *PaymentMethod) Unbind(now time.Time) {
	m.IsActive = false
	m.RecurringToken = ""
	m.BoundAt = now.UTC()
}
