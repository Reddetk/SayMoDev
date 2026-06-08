package redis

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
)

// ---------------------------------------------------------------------------
// Redis ключи, TTL и лимиты
// ---------------------------------------------------------------------------

const (
	// rl ключи
	rlIPKeyFmt      = "rl:ip:%s:hour"
	rlAccountKeyFmt = "rl:account:%s:day"

	// TTL Redis-счётчиков
	rlIPTTL      = 3600 * time.Second  // 1 час
	rlAccountTTL = 86400 * time.Second // 1 сутки

	// Пороги (Redis недоступен). Консервативные: 10 % от Redis-лимитов.
	// Обоснование: ин-процессный счётчик не разделяется между инстанциями, поэтому
	// порог должен быть значительно ниже Redis-лимита для fail-closed-защиты.
	rlFallbackIPLimit      int64 = 10 // 10 % от 100
	rlFallbackAccountLimit int64 = 5  // 10 % от 50
)

// ---------------------------------------------------------------------------
// in-process fallback счётчик
// ---------------------------------------------------------------------------

// fallbackCounter -- атомарный счётчик с TTL для одного ключа.
type fallbackCounter struct {
	count     atomic.Int64
	expiresAt atomic.Int64 // Unix nanoseconds
}

// increment увеличивает счётчик и возвращает новое значение.
// Если TTL истёк -- сбрасывает счётчик и выставляет новый окно.
func (c *fallbackCounter) increment(ttl time.Duration) int64 {
	now := time.Now()
	if now.UnixNano() > c.expiresAt.Load() {
		// TTL истёк -- сброс окна
		c.count.Store(0)
		c.expiresAt.Store(now.Add(ttl).UnixNano())
	}
	return c.count.Add(1)
}

// get возвращает текущее значение, учитывая TTL.
func (c *fallbackCounter) get() int64 {
	if time.Now().UnixNano() > c.expiresAt.Load() {
		return 0
	}
	return c.count.Load()
}

// ---------------------------------------------------------------------------
// RateLimiterAdapter
// ---------------------------------------------------------------------------

// RateLimiterAdapter реализует порт out.RateLimiter.
//
// Две стратегии хранения счётчиков:
//   - L1 (Redis): точный счётчик, разделяется между всеми инстанциями.
//   - L2 (in-process): fail-closed фоллбэк при недоступности Redis.
//       Счётчик локальный для процесса, порог = 10 % от Redis-лимита.
//
// Fail-closed инвариант: ошибка Redis никогда не означает "пропустить".
type RateLimiterAdapter struct {
	client redis.Cmdable

	// in-process fallback: sync.Map[string, *fallbackCounter]
	ipCounters      sync.Map
	accountCounters sync.Map
}

// NewRateLimiterAdapter создаёт адаптер с указанным Redis-клиентом.
func NewRateLimiterAdapter(client redis.Cmdable) *RateLimiterAdapter {
	return &RateLimiterAdapter{client: client}
}

// ---------------------------------------------------------------------------
// CheckIP
// ---------------------------------------------------------------------------

// CheckIP проверяет количество попыток с данного IP за последний час.
//
// Redis HIT: если счётчик >= MaxLoginAttemptsPerIPPerHour (100) -- ErrRateLimitIP.
// Redis недоступен: fail-closed через in-process счётчик (conserve порог = 10).
func (a *RateLimiterAdapter) CheckIP(ctx context.Context, clientIP string) error {
	key := fmt.Sprintf(rlIPKeyFmt, clientIP)
	count, err := a.getCounter(ctx, key)
	if err != nil {
		// fail-closed: используем in-process счётчик
		count = a.getFallbackIP(clientIP)
		if count >= rlFallbackIPLimit {
			return corerr.ErrRateLimitIP
		}
		return nil
	}
	if count >= maxLoginAttemptsPerIPPerHour {
		return corerr.ErrRateLimitIP
	}
	return nil
}

// ---------------------------------------------------------------------------
// CheckAccount
// ---------------------------------------------------------------------------

// CheckAccount проверяет количество неудачных попыток для аккаунта за сутки.
//
// Redis HIT: если счётчик >= MaxFailedLoginAttemptsPerDay (50) -- ErrRateLimitAccount.
// Redis недоступен: fail-closed через in-process счётчик (conserve порог = 5).
func (a *RateLimiterAdapter) CheckAccount(ctx context.Context, accountID string) error {
	key := fmt.Sprintf(rlAccountKeyFmt, accountID)
	count, err := a.getCounter(ctx, key)
	if err != nil {
		count = a.getFallbackAccount(accountID)
		if count >= rlFallbackAccountLimit {
			return corerr.ErrRateLimitAccount
		}
		return nil
	}
	if count >= maxFailedLoginAttemptsPerDay {
		return corerr.ErrRateLimitAccount
	}
	return nil
}

// ---------------------------------------------------------------------------
// RecordFailure
// ---------------------------------------------------------------------------

// RecordFailure инкрементирует счётчики неудачных попыток для IP и ({если задан) аккаунта.
//
// INCR rl:ip:<clientIP>:hour  -- всегда.
// INCR rl:account:<accountID>:day -- только если accountID != "".
// Оба команды отправляются в одном pipeline (не в транзакции).
// TTL устанавливается EXPIRE если INCR вернул 1 (ключ только что создан).
// Redis недоступен: инкрементируем in-process счётчики, возвращаем еррор вызывающему.
func (a *RateLimiterAdapter) RecordFailure(ctx context.Context, clientIP string, accountID string) error {
	ipKey := fmt.Sprintf(rlIPKeyFmt, clientIP)

	var (
		ipCmd      *redis.IntCmd
		accountCmd *redis.IntCmd
	)

	// Pipeline: INCR в одном раунд-трипе
	_, pipeErr := a.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		ipCmd = pipe.Incr(ctx, ipKey)
		if accountID != "" {
			accountKey := fmt.Sprintf(rlAccountKeyFmt, accountID)
			accountCmd = pipe.Incr(ctx, accountKey)
		}
		return nil
	})

	if pipeErr != nil {
		// Redis недоступен: fail-closed -- инкрементируем in-process счётчики
		a.incrementFallbackIP(clientIP)
		if accountID != "" {
			a.incrementFallbackAccount(accountID)
		}
		return fmt.Errorf("rateLimiter.RecordFailure: redis pipeline failed: %w", pipeErr)
	}

	// EXPIRE если ключ создан впервые (INCR -> 1)
	if ipCmd != nil && ipCmd.Val() == 1 {
		// Отдельный EXPIRE после pipeline: приемлемо -- INCR+EXPIRE -- 2 RTT,
		// но происходит редко (только при создании ключа -- первая попытка в окне)
		if expErr := a.client.Expire(ctx, ipKey, rlIPTTL).Err(); expErr != nil {
			// Некритическая ошибка: ключ записан, только без TTL (останется persistent).
			// Возвращаем ошибку, чтобы вызывающий мог залогировать.
			return fmt.Errorf("rateLimiter.RecordFailure: EXPIRE ip key failed: %w", expErr)
		}
	}

	if accountCmd != nil && accountCmd.Val() == 1 {
		accountKey := fmt.Sprintf(rlAccountKeyFmt, accountID)
		if expErr := a.client.Expire(ctx, accountKey, rlAccountTTL).Err(); expErr != nil {
			return fmt.Errorf("rateLimiter.RecordFailure: EXPIRE account key failed: %w", expErr)
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// getCounter -- чтение Redis-счётчика
// ---------------------------------------------------------------------------

// getCounter читает целочисленное значение ключа из Redis.
// Ключ отсутствует (redis.Nil): возвращает 0, nil.
// Redis недоступен: возвращает 0, err.
func (a *RateLimiterAdapter) getCounter(ctx context.Context, key string) (int64, error) {
	val, err := a.client.Get(ctx, key).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("rateLimiter.getCounter: %w", err)
	}
	return val, nil
}

// ---------------------------------------------------------------------------
// Fallback in-process хелперы
// ---------------------------------------------------------------------------

func (a *RateLimiterAdapter) getFallbackIP(clientIP string) int64 {
	v, _ := a.ipCounters.LoadOrStore(clientIP, &fallbackCounter{})
	return v.(*fallbackCounter).get()
}

func (a *RateLimiterAdapter) incrementFallbackIP(clientIP string) int64 {
	v, _ := a.ipCounters.LoadOrStore(clientIP, &fallbackCounter{})
	return v.(*fallbackCounter).increment(rlIPTTL)
}

func (a *RateLimiterAdapter) getFallbackAccount(accountID string) int64 {
	v, _ := a.accountCounters.LoadOrStore(accountID, &fallbackCounter{})
	return v.(*fallbackCounter).get()
}

func (a *RateLimiterAdapter) incrementFallbackAccount(accountID string) int64 {
	v, _ := a.accountCounters.LoadOrStore(accountID, &fallbackCounter{})
	return v.(*fallbackCounter).increment(rlAccountTTL)
}

// ---------------------------------------------------------------------------
// Константы лимитов -- вынесены в пакет consts
// ---------------------------------------------------------------------------

// TODO: перенести в identy-service/core/consts после создания пакета.
// Здесь временные пока consts-пакет не существует.
const (
	maxLoginAttemptsPerIPPerHour int64 = 100
	maxFailedLoginAttemptsPerDay int64 = 50
)

// ---------------------------------------------------------------------------
// Compile-time interface assertion
// ---------------------------------------------------------------------------

var _ interface {
	CheckIP(ctx context.Context, clientIP string) error
	CheckAccount(ctx context.Context, accountID string) error
	RecordFailure(ctx context.Context, clientIP string, accountID string) error
} = (*RateLimiterAdapter)(nil)
