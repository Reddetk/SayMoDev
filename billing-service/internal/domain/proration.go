package domain

import "fmt"

// CalculateProration считает доплату при upgrade внутри периода (§3, §10):
//
//	daily = ceil(price / billing_days)
//	charge = (new_daily - old_daily) * remaining_days
//
// charge ≤ 0 → ErrNotUpgrade; overflow/лимит → ErrOverflow/ErrAmountLimit.
func CalculateProration(oldPrice, newPrice Money, billingDays, remainingDays int) (Money, error) {
	if oldPrice.Currency != newPrice.Currency {
		return Money{}, fmt.Errorf("%w: currency mismatch %s/%s", ErrInvalidArgument, oldPrice.Currency, newPrice.Currency)
	}
	if err := validatePrice(oldPrice); err != nil {
		return Money{}, err
	}
	if err := validatePrice(newPrice); err != nil {
		return Money{}, err
	}
	if billingDays < 1 || billingDays > 365 {
		return Money{}, fmt.Errorf("%w: billing_days %d not in [1, 365]", ErrInvalidArgument, billingDays)
	}
	if remainingDays <= 0 {
		return Money{}, fmt.Errorf("%w: remaining_days must be positive", ErrPeriodExpired)
	}
	if remainingDays > billingDays {
		return Money{}, fmt.Errorf("%w: remaining_days %d exceeds billing_days %d", ErrInvalidArgument, remainingDays, billingDays)
	}

	oldDaily := ceilDiv(oldPrice.AmountKopecks, int64(billingDays))
	newDaily := ceilDiv(newPrice.AmountKopecks, int64(billingDays))
	diff := newDaily - oldDaily
	if diff <= 0 {
		return Money{}, ErrNotUpgrade
	}

	charge, ok := mulInt64(diff, int64(remainingDays))
	if !ok {
		return Money{}, ErrOverflow
	}
	if charge <= 0 {
		return Money{}, ErrNotUpgrade
	}
	if charge > MaxAmountKopecks {
		return Money{}, fmt.Errorf("%w: proration charge %d exceeds %d", ErrAmountLimit, charge, MaxAmountKopecks)
	}
	return Money{AmountKopecks: charge, Currency: oldPrice.Currency}, nil
}

func validatePrice(price Money) error {
	if !price.Currency.Valid() {
		return fmt.Errorf("%w: currency %q", ErrInvalidArgument, price.Currency)
	}
	if price.AmountKopecks < MinPriceKopecks || price.AmountKopecks > MaxAmountKopecks {
		return fmt.Errorf("%w: price %d out of range [%d, %d]", ErrAmountLimit, price.AmountKopecks, MinPriceKopecks, MaxAmountKopecks)
	}
	return nil
}

func ceilDiv(value, divisor int64) int64 {
	return (value + divisor - 1) / divisor
}
