package domain

import (
	"fmt"
	"time"
)

// WeekBitmap — 7-битная маска дней недели: бит 0 = понедельник, бит 6 = воскресенье.
type WeekBitmap uint8

// MaxWeekBitmap — максимальное валидное значение маски.
const MaxWeekBitmap = 127

// NewWeekBitmap валидирует маску [0, 127].
func NewWeekBitmap(value int) (WeekBitmap, error) {
	if value < 0 || value > MaxWeekBitmap {
		return 0, fmt.Errorf("%w: week bitmap %d out of range [0, %d]", ErrInvalidArgument, value, MaxWeekBitmap)
	}
	return WeekBitmap(value), nil
}

// TariffSnapshot — денормализованная копия правил тарифа (VO).
type TariffSnapshot struct {
	WeekBitmap WeekBitmap
	Timezone   string
}

// NewTariffSnapshot валидирует маску и IANA timezone.
func NewTariffSnapshot(bitmap WeekBitmap, timezone string) (TariffSnapshot, error) {
	if int(bitmap) < 0 || int(bitmap) > MaxWeekBitmap {
		return TariffSnapshot{}, fmt.Errorf("%w: week bitmap out of range", ErrInvalidArgument)
	}
	if timezone == "" {
		return TariffSnapshot{}, fmt.Errorf("%w: timezone is empty", ErrInvalidArgument)
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return TariffSnapshot{}, fmt.Errorf("%w: timezone %q: %v", ErrInvalidArgument, timezone, err)
	}
	return TariffSnapshot{WeekBitmap: bitmap, Timezone: timezone}, nil
}

// BillingPeriod — расчётный период (VO). В БД period_end считает PostgreSQL (§11),
// домен валидирует уже полученные значения.
type BillingPeriod struct {
	Start time.Time
	End   time.Time
}

// NewBillingPeriod валидирует period_end > period_start.
func NewBillingPeriod(start, end time.Time) (BillingPeriod, error) {
	if start.IsZero() || end.IsZero() {
		return BillingPeriod{}, fmt.Errorf("%w: billing period timestamps are zero", ErrInvalidArgument)
	}
	start = start.UTC()
	end = end.UTC()
	if !end.After(start) {
		return BillingPeriod{}, fmt.Errorf("%w: period_end must be after period_start", ErrInvalidArgument)
	}
	return BillingPeriod{Start: start, End: end}, nil
}

// Expired сообщает, истёк ли период на момент now (округление до минут, §11).
func (p BillingPeriod) Expired(now time.Time) bool {
	return !now.UTC().Truncate(time.Minute).Before(p.End.Truncate(time.Minute))
}

// RemainingDays — остаток периода в днях, округление вверх (proration).
func (p BillingPeriod) RemainingDays(now time.Time) int {
	now = now.UTC()
	if !now.Before(p.End) {
		return 0
	}
	hours := p.End.Sub(now).Hours()
	days := int(hours / 24)
	if hours-float64(days)*24 > 0 {
		days++
	}
	return days
}

// ScheduledChange — запланированный downgrade на следующий период.
type ScheduledChange struct {
	TargetTariffPlanID UUID
	EffectiveAt        time.Time
}

// TariffPlan — иммутабельный каталог правил доступа.
type TariffPlan struct {
	ID        UUID
	Name      string
	Snapshot  TariffSnapshot
	CreatedAt time.Time
}

// SubscriptionOffer — ценовое предложение тарифа, версионируется valid_from/valid_until.
type SubscriptionOffer struct {
	ID           UUID
	TariffPlanID UUID
	Price        Money
	BillingDays  int
	ValidFrom    time.Time
	ValidUntil   *time.Time
	IsActive     bool
}

// Validate проверяет инварианты оффера §9–§10.
func (o SubscriptionOffer) Validate() error {
	if o.ID == (UUID{}) || o.TariffPlanID == (UUID{}) {
		return fmt.Errorf("%w: offer ids are required", ErrInvalidArgument)
	}
	if o.BillingDays < 1 || o.BillingDays > 365 {
		return fmt.Errorf("%w: billing_days %d not in [1, 365]", ErrInvalidArgument, o.BillingDays)
	}
	if _, err := NewPrice(o.Price.AmountKopecks, o.Price.Currency); err != nil {
		return err
	}
	if o.ValidFrom.IsZero() {
		return fmt.Errorf("%w: valid_from is required", ErrInvalidArgument)
	}
	if o.ValidUntil != nil && !o.ValidUntil.After(o.ValidFrom) {
		return fmt.Errorf("%w: valid_until must be after valid_from", ErrInvalidArgument)
	}
	return nil
}

// ActiveAt сообщает, действует ли оффер в момент t.
func (o SubscriptionOffer) ActiveAt(t time.Time) bool {
	if !o.IsActive {
		return false
	}
	if t.Before(o.ValidFrom) {
		return false
	}
	return o.ValidUntil == nil || t.Before(*o.ValidUntil)
}
