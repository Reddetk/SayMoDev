package out

import "context"

// RateLimiter -- out-port для rate limiting аутентификации
//
// Порядок проверок в AuthService.Login строго фиксирован (§5 Auth Invariants):
//   [1] CheckIP -- до любого обращения к БД (anti-scraping)
//   [4] CheckAccount -- после успешного lookup, до bcrypt
//   [5] RecordFailure -- после провала bcrypt.Compare
//
// Лимиты определены в consts:
//   - MaxLoginAttemptsPerIPPerHour    = 100
//   - MaxFailedLoginAttemptsPerDay    = 50
//   - AccountLockDurationSeconds      = 86400
//
// Redis ключи (управляются адаптером):
//   - rl:ip:<clientIP>:hour
//   - rl:account:<accountID>:day
type RateLimiter interface {
	// CheckIP проверяет количество попыток с данного IP за последний час
	// Возвращает ErrRateLimitIP при превышении лимита
	// Вызывается первым -- до EmailExist и bcrypt
	CheckIP(ctx context.Context, clientIP string) error

	// CheckAccount проверяет количество неудачных попыток для аккаунта за сутки
	// Возвращает ErrRateLimitAccount при превышении лимита
	// Вызывается после FindByEmail, до bcrypt.Compare
	CheckAccount(ctx context.Context, accountID string) error

	// RecordFailure инкрементирует счётчики неудачных попыток
	// Вызывается только после провала bcrypt.Compare (шаг [5])
	// accountID может быть пустой строкой если аккаунт не найден (только IP счётчик)
	RecordFailure(ctx context.Context, clientIP string, accountID string) error
}
