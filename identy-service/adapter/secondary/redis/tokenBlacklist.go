package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	blacklistKeyPrefix  = "jwt:black:"
	accountRevKeyPrefix = "account:rev:"
	accountRevTTL       = 30 * 24 * time.Hour // 30 дней -- max token lifetime

	l1TTL      = 60 * time.Second
	l1Capacity = 10_000
)

// l1Entry -- запись L1 in-memory кеша
type l1Entry struct {
	blacklisted bool
	expiresAt   time.Time
}

// TokenBlacklistAdapter реализует port/out.TokenBlacklist.
//
// Слои:
//   - L1: in-process sync.Map с TTL 60s -- снижает нагрузку на Redis для hot jti
//   - L2: Redis -- jwt:black:<jti> и account:rev:<accountID>
//
// Fail-closed: любая ошибка Redis в Contains и GetAccountRev возвращается
// вызывающему; TokenService интерпретирует её как ErrTokenRevoked (401).
type TokenBlacklistAdapter struct {
	client redis.Cmdable

	l1mu    sync.RWMutex
	l1cache map[string]l1Entry
}

func NewTokenBlacklistAdapter(client redis.Cmdable) *TokenBlacklistAdapter {
	return &TokenBlacklistAdapter{
		client:  client,
		l1cache: make(map[string]l1Entry, l1Capacity),
	}
}

// Add записывает jti в Redis blacklist (L2).
// TTL = expiresAtUnix - now; если TTL <= 0 -- no-op (токен уже истёк).
func (a *TokenBlacklistAdapter) Add(ctx context.Context, jti string, expiresAtUnix int64) error {
	ttl := time.Duration(expiresAtUnix-time.Now().Unix()) * time.Second
	if ttl <= 0 {
		return nil
	}
	key := blacklistKeyPrefix + jti
	return a.client.Set(ctx, key, "1", ttl).Err()
}

// Contains проверяет наличие jti в blacklist: L1 -> L2.
//
// L1 HIT blacklisted: возвращает true, nil без обращения к Redis.
// L1 HIT valid (не в blacklist, не истёк): возвращает false, nil.
// L1 MISS: запрос в Redis L2.
// Redis недоступен: возвращает false, err (fail-closed в TokenService).
func (a *TokenBlacklistAdapter) Contains(ctx context.Context, jti string) (bool, error) {
	// L1 lookup
	a.l1mu.RLock()
	entry, ok := a.l1cache[jti]
	a.l1mu.RUnlock()

	if ok && time.Now().Before(entry.expiresAt) {
		return entry.blacklisted, nil
	}

	// L2 Redis lookup
	key := blacklistKeyPrefix + jti
	_, err := a.client.Get(ctx, key).Result()
	if err == nil {
		// ключ найден -- токен в blacklist; кешируем в L1
		a.setL1(jti, true)
		return true, nil
	}
	if errors.Is(err, redis.Nil) {
		// ключ не найден -- токен не отозван; кешируем положительный результат в L1
		a.setL1(jti, false)
		return false, nil
	}
	// Redis недоступен
	return false, fmt.Errorf("tokenBlacklist.Contains: redis unavailable: %w", err)
}

// GetAccountRev возвращает текущий rev аккаунта из Redis L2.
//
// L2 miss (ключ отсутствует): rev не установлен → возвращает 0, nil.
//   Интерпретация: mass-revoke не был инициирован; токен валиден по rev.
//
// Redis недоступен: возвращает 0, err → TokenService вернёт ErrTokenRevoked (fail-closed).
func (a *TokenBlacklistAdapter) GetAccountRev(ctx context.Context, accountID string) (int64, error) {
	key := accountRevKeyPrefix + accountID
	val, err := a.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		// ключ отсутствует -- rev не установлен, mass-revoke не был
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("tokenBlacklist.GetAccountRev: redis unavailable: %w", err)
	}
	rev, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("tokenBlacklist.GetAccountRev: invalid rev value %q: %w", val, err)
	}
	return rev, nil
}

// SetAccountRev записывает текущий rev аккаунта в Redis L2.
// Вызывается при LockAccount, ChangePassword, SoftDelete (любом mass-revoke).
// TTL = 30 дней (max token lifetime).
//
// Не является частью port/out.TokenBlacklist -- это внутренний метод адаптера,
// вызываемый из других адаптеров (AccountRepository adapter) после rev++ транзакции.
func (a *TokenBlacklistAdapter) SetAccountRev(ctx context.Context, accountID string, rev int64) error {
	key := accountRevKeyPrefix + accountID
	return a.client.Set(ctx, key, strconv.FormatInt(rev, 10), accountRevTTL).Err()
}

// setL1 записывает jti в L1 кеш с TTL 60s.
// Простое LRU не реализовано -- при достижении l1Capacity новые записи не кешируются.
func (a *TokenBlacklistAdapter) setL1(jti string, blacklisted bool) {
	a.l1mu.Lock()
	defer a.l1mu.Unlock()
	if len(a.l1cache) >= l1Capacity {
		return
	}
	a.l1cache[jti] = l1Entry{
		blacklisted: blacklisted,
		expiresAt:   time.Now().Add(l1TTL),
	}
}
