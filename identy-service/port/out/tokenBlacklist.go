package out

import "context"

// TokenBlacklist -- out-port для управления blacklist отозванных jti
//
// Архитектура хранения (G9 Revocation Write Order):
//   - L1: in-memory cache (60s TTL) -- внутри адаптера, невидим порту
//   - L2: Redis -- Add и Contains работают через этот слой
//   - L3: PostgreSQL (source of truth) -- пишется через outbox в AccountRepository
//
// AuthService пишет в L2 напрямую после успешного коммита транзакции.
// Outbox-воркер реплицирует L2 -> L3 асинхронно.
// При промахе L2 адаптер должен упасть с ошибкой (не silent fail).
type TokenBlacklist interface {
	// Add записывает jti в Redis blacklist
	// expiresAtUnix -- unix timestamp истечения токена (exp claim)
	// TTL в Redis вычисляется адаптером как expiresAtUnix - time.Now().Unix()
	// Если TTL <= 0 -- Add является no-op (токен уже истёк)
	Add(ctx context.Context, jti string, expiresAtUnix int64) error

	// Contains проверяет наличие jti в blacklist (L1 -> L2)
	// Используется в middleware валидации токена
	Contains(ctx context.Context, jti string) (bool, error)

	// GetAccountRev возвращает текущий rev аккаунта из Redis L2.
	// Ключ: account:rev:<accountID>, TTL 30 дней (обновляется при каждом rev++).
	//
	// L2 miss (ключ отсутствует): возвращает 0, nil.
	//   Семантика: rev в Redis не установлен -- mass-revoke не был инициирован.
	//   TokenService интерпретирует 0 как "rev не отозван" и продолжает.
	//
	// Redis недоступен: возвращает 0, err.
	//   TokenService должен вернуть ErrTokenRevoked (fail-closed по спецификации BC#1 Step 3).
	GetAccountRev(ctx context.Context, accountID string) (int64, error)
}
