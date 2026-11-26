package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// ParsePrice разбирает цену из внешнего представления (JSON-строка/вебхук).
// Строгий формат: только десятичные цифры, без пробелов, знака и ведущих нулей.
func ParsePrice(rawAmount, rawCurrency string) (Money, error) {
	amount, err := parseKopecks(rawAmount, "amount")
	if err != nil {
		return Money{}, err
	}
	return NewPrice(amount, Currency(rawCurrency))
}

// ParsePaymentAmount разбирает сумму платежа.
func ParsePaymentAmount(rawAmount, rawCurrency string) (Money, error) {
	amount, err := parseKopecks(rawAmount, "amount")
	if err != nil {
		return Money{}, err
	}
	return NewPaymentAmount(amount, Currency(rawCurrency))
}

func parseKopecks(raw, field string) (int64, error) {
	if raw == "" {
		return 0, fmt.Errorf("%w: %s is empty", ErrInvalidArgument, field)
	}
	if strings.ContainsAny(raw, ".eE") {
		return 0, fmt.Errorf("%w: %s must be an integer", ErrInvalidArgument, field)
	}
	if raw[0] == '+' {
		return 0, fmt.Errorf("%w: %s must not have a sign", ErrInvalidArgument, field)
	}
	digits := raw
	if raw[0] == '-' {
		digits = raw[1:]
	}
	if digits == "" {
		return 0, fmt.Errorf("%w: %s must be an integer", ErrInvalidArgument, field)
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("%w: %s must contain only digits", ErrInvalidArgument, field)
		}
	}
	if len(digits) > 1 && digits[0] == '0' {
		return 0, fmt.Errorf("%w: %s has a leading zero", ErrInvalidArgument, field)
	}
	amount, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s", ErrOverflow, field)
	}
	return amount, nil
}

// ParseWeekBitmap разбирает 7-битную маску из строки.
func ParseWeekBitmap(raw string) (WeekBitmap, error) {
	if raw == "" {
		return 0, fmt.Errorf("%w: bitmap is empty", ErrInvalidArgument)
	}
	digits := raw
	if raw[0] == '-' {
		digits = raw[1:]
	}
	if digits == "" {
		return 0, fmt.Errorf("%w: bitmap must be an integer", ErrInvalidArgument)
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("%w: bitmap must be an integer", ErrInvalidArgument)
		}
	}
	if len(digits) > 1 && digits[0] == '0' {
		return 0, fmt.Errorf("%w: bitmap has a leading zero", ErrInvalidArgument)
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: bitmap out of range", ErrInvalidArgument)
	}
	return NewWeekBitmap(int(value))
}

// CalculateProrationFromStrings — граница для HTTP/JSON: разбирает строки,
// затем применяет доменный расчёт.
func CalculateProrationFromStrings(oldRaw, newRaw, billingDaysRaw, remainingRaw string) (Money, error) {
	oldPrice, err := parseProrationPrice(oldRaw, "old price")
	if err != nil {
		return Money{}, err
	}
	newPrice, err := parseProrationPrice(newRaw, "new price")
	if err != nil {
		return Money{}, err
	}
	billingDays, err := parseStrictInt(billingDaysRaw, "billing_days")
	if err != nil {
		return Money{}, err
	}
	remainingDays, err := parseStrictInt(remainingRaw, "remaining_days")
	if err != nil {
		return Money{}, err
	}
	return CalculateProration(oldPrice, newPrice, int(billingDays), int(remainingDays))
}

func parseProrationPrice(raw, field string) (Money, error) {
	if raw == "" {
		return Money{}, fmt.Errorf("%w: %s is empty", ErrInvalidArgument, field)
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			if strings.ContainsAny(raw, ".eE") {
				return Money{}, fmt.Errorf("%w: %s must be an integer", ErrInvalidArgument, field)
			}
			return Money{}, fmt.Errorf("%w: %s must be an integer", ErrInvalidArgument, field)
		}
	}
	amount, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return Money{}, fmt.Errorf("%w: %s", ErrOverflow, field)
	}
	return NewPrice(amount, CurrencyBYN)
}

func parseStrictInt(raw, field string) (int64, error) {
	if raw == "" {
		return 0, fmt.Errorf("%w: %s is empty", ErrInvalidArgument, field)
	}
	digits := raw
	if raw[0] == '-' {
		digits = raw[1:]
	}
	if digits == "" {
		return 0, fmt.Errorf("%w: %s must be an integer", ErrInvalidArgument, field)
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("%w: %s must be an integer", ErrInvalidArgument, field)
		}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s", ErrOverflow, field)
	}
	return value, nil
}
