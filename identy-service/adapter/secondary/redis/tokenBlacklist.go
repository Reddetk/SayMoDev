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

// ---------------------------------------------------------------------------
// Key prefixes и константы
// ---------------------------------------------------------------------------

const (
	// blacklistKeyPrefix -- префикс ключа отозванного jti в Redis L2.
	// Полный ключ: "jwt:black:<jti>"
	blacklistKeyPrefix = "jwt:black:"

	// accountRevKeyPrefix -- префикс ключа rev аккаунта в Redis L2.
	// Полный ключ: "account:rev:<accountID>"
	accountRevKeyPrefix = "account:rev:"

	// accountRevTTL -- TTL ключа rev в Redis.
	// 30 дней = max token lifetime; после истечения все токены всё равно истекут, rev больше не нужен.
	accountRevTTL = 30 * 24 * time.Hour

	// l1TTL -- время жизни записи в L1 in-memory кеше.
	// Кешируются только отозванные (blacklisted=true) jti для снижения нагрузки на Redis.
	l1TTL = 60 * time.Second

	// l1Capacity -- максимальное количество записей в L1.
	// При превышении выполняется eviction старейших записей (FIFO по expiresAt).
	l1Capacity = 10_000
)

// ---------------------------------------------------------------------------
// L1 кеш: запись
// ---------------------------------------------------------------------------

// l1Entry -- запись L1 in-memory кеша.
// Хранит только blacklisted=true записи (HIT в Redis).
type l1Entry struct {
	expiresAt time.Time
}

// ---------------------------------------------------------------------------
// TokenBlacklistAdapter
// ---------------------------------------------------------------------------

// TokenBlacklistAdapter реализует порт out.TokenBlacklist.
//
// Архитектура кеширования для jti:
//   - L1: in-process sync.Map-подобный кеш, TTL 60s.
//       Кеширует ТОЛЬКО blacklisted=true записи.
//       Miss -- не значает "не в blacklist": нужно спросить Redis.
//   - L2: Redis. jwt:black:<jti> -- отозванные jti;
//                account:rev:<accountID> -- rev для mass-revoke.
//
// Fail-closed: ошибка Redis в Contains и GetAccountRev возвращается
// вызывающему; TokenService интерпретирует её как ErrTokenRevoked (HTTP 401).
type TokenBlacklistAdapter struct {
	client redis.Cmdable

	l1mu  sync.RWMutex
	l1    map[string]l1Entry // ключ: jti, значение всегда blacklisted=true
	l1Cnt int               // текущий размер кеша
}

// NewTokenBlacklistAdapter создаёт адаптер с указанным Redis-клиентом.
// client принимает redis.Cmdable для совместимости с *redis.Client и *redis.ClusterClient.
func NewTokenBlacklistAdapter(client redis.Cmdable) *TokenBlacklistAdapter {
	return &TokenBlacklistAdapter{
		client: client,
		l1:     make(map[string]l1Entry, l1Capacity),
	}
}

// ---------------------------------------------------------------------------
// Add
// ---------------------------------------------------------------------------

// Add записывает jti в Redis blacklist (L2 напрямую, L1 не пополняется).
//
// TTL = expiresAtUnix - now; если TTL <= 0 -- no-op (токен уже истёк).
// Redis недоступен -- возвращает обёрнутую ошибку (не проглатывает).
func (a *TokenBlacklistAdapter) Add(ctx context.Context, jti string, expiresAtUnix int64) error {
	ttl := time.Duration(expiresAtUnix-time.Now().Unix()) * time.Second
	if ttl <= 0 {
		// Токен уже истёк -- добавлять в blacklist нецелесообразно
		return nil
	}
	key := blacklistKeyPrefix + jti
	if err := a.client.Set(ctx, key, "1", ttl).Err(); err != nil {
		return fmt.Errorf("tokenBlacklist.Add: redis SET failed: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Contains
// ---------------------------------------------------------------------------

// Contains проверяет наличие jti в blacklist: L1 -> L2.
//
// L1 HIT (jti найден и TTL не истёк): (true, nil) без запроса в Redis.
// L1 MISS: запрос в Redis EXISTS.
//   Redis HIT:         пишем в L1, возвращаем (true, nil).
//   Redis MISS:        возвращаем (false, nil). L1 не пополняется.
//   Redis недоступен: возвращаем (false, err). Fail-closed в TokenService.
//
// Инвариант: L1 не кеширует "false" (не в blacklist).
// Если jti был добавлен через Add после L1 MISS, Redis найдёт его на следующем запросе.
func (a *TokenBlacklistAdapter) Contains(ctx context.Context, jti string) (bool, error) {
	// L1 lookup
	a.l1mu.RLock()
	entry, hit := a.l1[jti]
	a.l1mu.RUnlock()

	if hit && time.Now().Before(entry.expiresAt) {
		// L1 HIT: jti в blacklist, TTL ещё актуален
		return true, nil
	}

	// L2 Redis: EXISTS -- не читаем значение, только проверяем наличие ключа
	key := blacklistKeyPrefix + jti
	count, err := a.client.Exists(ctx, key).Result()
	if err != nil {
		// Redis недоступен -- fail-closed: TokenService должен вернуть ErrTokenRevoked
		return false, fmt.Errorf("tokenBlacklist.Contains: redis unavailable: %w", err)
	}

	if count > 0 {
		// Redis HIT: токен в blacklist -- пополняем L1
		a.setL1(jti)
		return true, nil
	}

	// Redis MISS: токен не отозван -- L1 не пополняем
	return false, nil
}

// ---------------------------------------------------------------------------
// GetAccountRev
// ---------------------------------------------------------------------------

// GetAccountRev возвращает текущий rev аккаунта из Redis L2.
//
// L2 miss (ключ отсутствует): mass-revoke не инициировался -- возвращает (0, nil).
// TokenService интерпретирует 0 как "не отозван" и продолжает обработку.
// Redis недоступен: возвращает (0, err) -- fail-closed в TokenService.
// L1 не используется: rev нужен актуальным на каждый запрос.
func (a *TokenBlacklistAdapter) GetAccountRev(ctx context.Context, accountID string) (int64, error) {
	key := accountRevKeyPrefix + accountID
	val, err := a.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		// L2 miss: rev не установлен -- mass-revoke не был
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("tokenBlacklist.GetAccountRev: redis unavailable: %w", err)
	}
	rev, parseErr := strconv.ParseInt(val, 10, 64)
	if parseErr != nil {
		// Невалидное значение в Redis -- возвращаем цепочку ошибки
		return 0, fmt.Errorf("tokenBlacklist.GetAccountRev: corrupted rev value %q in Redis: %w", val, parseErr)
	}
	return rev, nil
}

// ---------------------------------------------------------------------------
// SetAccountRev
// ---------------------------------------------------------------------------

// SetAccountRev записывает текущий rev аккаунта в Redis L2.
// Вызывается после LockAccount, ChangePassword, SoftDelete (любого mass-revoke).
// TTL = 30 дней (max token lifetime).
// Redis недоступен: возвращает err. Вызывающий AccountService логирует как WARN
// (транзакция уже закоммичена, rev в PostgreSQL актуален; деградация только до L3 lookup).
func (a *TokenBlacklistAdapter) SetAccountRev(ctx context.Context, accountID string, rev int64) error {
	key := accountRevKeyPrefix + accountID
	if err := a.client.Set(ctx, key, strconv.FormatInt(rev, 10), accountRevTTL).Err(); err != nil {
		return fmt.Errorf("tokenBlacklist.SetAccountRev: redis SET failed: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// internal: L1 helpers
// ---------------------------------------------------------------------------

// setL1 записывает jti в L1 кеш с TTL l1TTL.
//
// Инвариант: кешируются ТОЛЬКО blacklisted=true записи.
// Если кеш заполнен (l1Cnt >= l1Capacity) -- выполняется eviction одной истекшей записи.
func (a *TokenBlacklistAdapter) setL1(jti string) {
	a.l1mu.Lock()
	defer a.l1mu.Unlock()

	// Evict одну истекшую запись если кеш полон
	if a.l1Cnt >= l1Capacity {
		a.evictOneExpired()
	}
	// Если после eviction всё ещё полн -- пропускаем запись в L1.
	// Redis знает правду -- высокая нагрузка сохраняет корректность.
	if a.l1Cnt >= l1Capacity {
		return
	}

	_, existed := a.l1[jti]
	a.l1[jti] = l1Entry{expiresAt: time.Now().Add(l1TTL)}
	if !existed {
		a.l1Cnt++
	}
}

// evictOneExpired удаляет одну истекшую запись из L1.
// Вызывается под write lock (изнутри setL1).
// Проходит по map до первой истекшей записи -- O(n) в худшем случае,
// но вызывается редко (только при переполнении).
func (a *TokenBlacklistAdapter) evictOneExpired() {
	now := time.Now()
	for k, v := range a.l1 {
		if now.After(v.expiresAt) {
			delete(a.l1, k)
			a.l1Cnt--
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Compile-time interface assertion
// ---------------------------------------------------------------------------

// Гарантирует, что TokenBlacklistAdapter реализует порт out.TokenBlacklist целиком.
// Импорт порта избегается для предотвращения import cycle;
// проверка выполняется через inline-интерфейс.
var _ interface {
	Add(ctx context.Context, jti string, expiresAtUnix int64) error
	Contains(ctx context.Context, jti string) (bool, error)
	GetAccountRev(ctx context.Context, accountID string) (int64, error)
	SetAccountRev(ctx context.Context, accountID string, rev int64) error
} = (*TokenBlacklistAdapter)(nil)
