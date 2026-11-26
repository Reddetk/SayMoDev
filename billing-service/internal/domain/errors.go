// Package domain — агрегаты и value objects BC#2 Billing and Subscriptions.
//
// Инварианты §1–§12 из classifications/BillingandSubscriptionsManagement.md
// живут здесь, а не в handler/repository.
package domain

// goca:manual — доменная модель BC#2; goca feature генерирует только CRUD-заготовку.

import "errors"

var (
	// ErrInvalidTransition — переход статуса запрещён инвариантами §1–§8.
	ErrInvalidTransition = errors.New("invalid subscription transition")
	// ErrPaymentRequired — активация без success-платежа (§5).
	ErrPaymentRequired = errors.New("payment must be success before activation")
	// ErrPeriodExpired — период истёк, операция невозможна.
	ErrPeriodExpired = errors.New("billing period expired")
	// ErrNotUpgrade — proration ≤ 0, target не дороже текущего тарифа.
	ErrNotUpgrade = errors.New("proration charge is not positive: target tariff is not an upgrade")
	// ErrAmountLimit — сумма вне допустимого диапазона §10.
	ErrAmountLimit = errors.New("amount out of range")
	// ErrOverflow — переполнение int64 при расчёте §10.
	ErrOverflow = errors.New("int64 overflow")
	// ErrInvalidArgument — некорректный входной аргумент.
	ErrInvalidArgument = errors.New("invalid argument")
)
