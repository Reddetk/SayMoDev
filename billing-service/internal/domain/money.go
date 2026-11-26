package domain

import "fmt"

// Денежные лимиты (инвариант §10). Все суммы — BIGINT в копейках.
const (
	MinPriceKopecks  int64 = 100
	MinAmountKopecks int64 = 1
	MaxAmountKopecks int64 = 100_000_000
)

// Currency — разрешённые валюты.
type Currency string

const (
	CurrencyBYN Currency = "BYN"
	CurrencyRUB Currency = "RUB"
	CurrencyUSD Currency = "USD"
	CurrencyEUR Currency = "EUR"
)

// Valid сообщает, разрешена ли валюта.
func (c Currency) Valid() bool {
	switch c {
	case CurrencyBYN, CurrencyRUB, CurrencyUSD, CurrencyEUR:
		return true
	default:
		return false
	}
}

// Money — VO суммы в копейках.
type Money struct {
	AmountKopecks int64
	Currency      Currency
}

// NewMoney создаёт Money с проверкой валюты и диапазона [min, MaxAmountKopecks].
func NewMoney(amountKopecks int64, currency Currency, min int64) (Money, error) {
	if !currency.Valid() {
		return Money{}, fmt.Errorf("%w: currency %q", ErrInvalidArgument, currency)
	}
	if amountKopecks < min || amountKopecks > MaxAmountKopecks {
		return Money{}, fmt.Errorf("%w: amount %d out of range [%d, %d]", ErrAmountLimit, amountKopecks, min, MaxAmountKopecks)
	}
	return Money{AmountKopecks: amountKopecks, Currency: currency}, nil
}

// NewPrice создаёт Money для цены оффера (минимум 1 рубль).
func NewPrice(amountKopecks int64, currency Currency) (Money, error) {
	money, err := NewMoney(amountKopecks, currency, MinPriceKopecks)
	if err != nil {
		return Money{}, fmt.Errorf("price: %w", err)
	}
	return money, nil
}

// NewPaymentAmount создаёт Money для суммы платежа (минимум 1 копейка).
func NewPaymentAmount(amountKopecks int64, currency Currency) (Money, error) {
	money, err := NewMoney(amountKopecks, currency, MinAmountKopecks)
	if err != nil {
		return Money{}, fmt.Errorf("payment amount: %w", err)
	}
	return money, nil
}

// Add складывает деньги одной валюты с проверкой overflow.
func (m Money) Add(other Money) (Money, error) {
	if m.Currency != other.Currency {
		return Money{}, fmt.Errorf("%w: currency mismatch %s/%s", ErrInvalidArgument, m.Currency, other.Currency)
	}
	sum, ok := addInt64(m.AmountKopecks, other.AmountKopecks)
	if !ok {
		return Money{}, ErrOverflow
	}
	if sum > MaxAmountKopecks {
		return Money{}, ErrAmountLimit
	}
	return Money{AmountKopecks: sum, Currency: m.Currency}, nil
}

func addInt64(a, b int64) (int64, bool) {
	sum := a + b
	if (b > 0 && sum < a) || (b < 0 && sum > a) {
		return 0, false
	}
	return sum, true
}

func mulInt64(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	product := a * b
	if product/b != a {
		return 0, false
	}
	return product, true
}
