// Package redis stands for redis secondary adapter implementation
package redis

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	consts "github.com/Reddetk/SayMoDev/identy-service/core/consts"
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

	rateLimiterTracerName = "identy-service/adapter/redis-rate-limiter"
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
// Если TTL истёк -- сбрасывает счётчик и выставляет новое окно.
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
	logger *slog.Logger
	tracer trace.Tracer

	// in-process fallback: sync.Map[string, *fallbackCounter]
	ipCounters      sync.Map
	accountCounters sync.Map
}

// NewRateLimiterAdapter создаёт адаптер с указанным Redis-клиентом.
// logger nil -- используется slog.Default().
func NewRateLimiterAdapter(client redis.Cmdable, logger *slog.Logger) *RateLimiterAdapter {
	if logger == nil {
		logger = slog.Default()
	}
	return &RateLimiterAdapter{
		client: client,
		logger: logger,
		tracer: otel.Tracer(rateLimiterTracerName),
	}
}

// ---------------------------------------------------------------------------
// CheckIP
// ---------------------------------------------------------------------------

// CheckIP проверяет количество попыток с данного IP за последний час.
//
// Redis HIT: если счётчик >= consts.MaxLoginAttemptsPerIPPerHour (100) -- ErrRateLimitIP.
// Redis недоступен: fail-closed через in-process счётчик (conserve порог = 10).
func (a *RateLimiterAdapter) CheckIP(ctx context.Context, clientIP string) error {
	ctx, span := a.tracer.Start(ctx, "rateLimiter.CheckIP",
		trace.WithAttributes(attribute.String("client.ip", clientIP)),
	)
	defer span.End()

	key := fmt.Sprintf(rlIPKeyFmt, clientIP)
	count, err := a.getCounter(ctx, key)
	if err != nil {
		// Redis недоступен: warn + fail-closed fallback
		a.logger.WarnContext(ctx, "rateLimiter: CheckIP redis unavailable, using fallback",
			slog.String("client_ip", clientIP),
			slog.String("error", err.Error()),
		)
		span.SetAttributes(attribute.Bool("fallback", true))
		count = a.getFallbackIP(clientIP)
		if count >= rlFallbackIPLimit {
			span.SetStatus(codes.Error, "rate limit exceeded (fallback)")
			return corerr.ErrRateLimitIP
		}
		return nil
	}

	span.SetAttributes(attribute.Int64("counter", count))
	if count >= consts.MaxLoginAttemptsPerIPPerHour {
		span.SetStatus(codes.Error, "rate limit exceeded")
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
	ctx, span := a.tracer.Start(ctx, "rateLimiter.CheckAccount",
		trace.WithAttributes(attribute.String("account.id", accountID)),
	)
	defer span.End()

	key := fmt.Sprintf(rlAccountKeyFmt, accountID)
	count, err := a.getCounter(ctx, key)
	if err != nil {
		a.logger.WarnContext(ctx, "rateLimiter: CheckAccount redis unavailable, using fallback",
			slog.String("account_id", accountID),
			slog.String("error", err.Error()),
		)
		span.SetAttributes(attribute.Bool("fallback", true))
		count = a.getFallbackAccount(accountID)
		if count >= rlFallbackAccountLimit {
			span.SetStatus(codes.Error, "rate limit exceeded (fallback)")
			return corerr.ErrRateLimitAccount
		}
		return nil
	}

	span.SetAttributes(attribute.Int64("counter", count))
	if count >= consts.MaxFailedLoginAttemptsPerDay {
		span.SetStatus(codes.Error, "rate limit exceeded")
		return corerr.ErrRateLimitAccount
	}
	return nil
}

// ---------------------------------------------------------------------------
// RecordFailure
// ---------------------------------------------------------------------------

// RecordFailure инкрементирует счётчики неудачных попыток для IP и (если задан) аккаунта.
//
// INCR rl:ip:<clientIP>:hour  -- всегда.
// INCR rl:account:<accountID>:day -- только если accountID != "".
// Оба команды отправляются в одном pipeline (не в транзакции).
// TTL устанавливается EXPIRE если INCR вернул 1 (ключ только что создан).
// Redis недоступен: инкрементируем in-process счётчики, логируем ERROR, возвращаем ошибку.
func (a *RateLimiterAdapter) RecordFailure(ctx context.Context, clientIP string, accountID string) error {
	ctx, span := a.tracer.Start(ctx, "rateLimiter.RecordFailure",
		trace.WithAttributes(
			attribute.String("client.ip", clientIP),
			attribute.String("account.id", accountID),
		),
	)
	defer span.End()

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
		span.RecordError(pipeErr)
		span.SetStatus(codes.Error, pipeErr.Error())
		span.SetAttributes(attribute.Bool("fallback", true))
		a.logger.ErrorContext(ctx, "rateLimiter: RecordFailure redis pipeline failed, falling back to in-process counters",
			slog.String("client_ip", clientIP),
			slog.String("account_id", accountID),
			slog.String("error", pipeErr.Error()),
		)
		a.incrementFallbackIP(clientIP)
		if accountID != "" {
			a.incrementFallbackAccount(accountID)
		}
		return fmt.Errorf("rateLimiter.RecordFailure: redis pipeline failed: %w", pipeErr)
	}

	// EXPIRE если ключ создан впервые (INCR -> 1)
	if ipCmd != nil && ipCmd.Val() == 1 {
		if expErr := a.client.Expire(ctx, ipKey, rlIPTTL).Err(); expErr != nil {
			// Некритическая ошибка: ключ записан без TTL (станет persistent).
			span.RecordError(expErr)
			a.logger.ErrorContext(ctx, "rateLimiter: EXPIRE ip key failed -- key will be persistent",
				slog.String("client_ip", clientIP),
				slog.String("key", ipKey),
				slog.String("error", expErr.Error()),
			)
			return fmt.Errorf("rateLimiter.RecordFailure: EXPIRE ip key failed: %w", expErr)
		}
	}

	if accountCmd != nil && accountCmd.Val() == 1 {
		accountKey := fmt.Sprintf(rlAccountKeyFmt, accountID)
		if expErr := a.client.Expire(ctx, accountKey, rlAccountTTL).Err(); expErr != nil {
			span.RecordError(expErr)
			a.logger.ErrorContext(ctx, "rateLimiter: EXPIRE account key failed -- key will be persistent",
				slog.String("account_id", accountID),
				slog.String("key", accountKey),
				slog.String("error", expErr.Error()),
			)
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
// Compile-time interface assertion
// ---------------------------------------------------------------------------

var _ interface {
	CheckIP(ctx context.Context, clientIP string) error
	CheckAccount(ctx context.Context, accountID string) error
	RecordFailure(ctx context.Context, clientIP string, accountID string) error
} = (*RateLimiterAdapter)(nil)
