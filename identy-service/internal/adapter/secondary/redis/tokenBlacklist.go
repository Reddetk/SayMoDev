package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/Reddetk/SayMoDev/identy-service/internal/logger"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

//
// Key prefixes и константы
//

const (
	// blacklistKeyPrefix  префикс ключа отозванного jti в Redis L2.
	// Полный ключ: "jwt:black:<jti>"
	blacklistKeyPrefix = "jwt:black:"

	// accountRevKeyPrefix  префикс ключа rev аккаунта в Redis L2.
	// Полный ключ: "account:rev:<accountID>"
	accountRevKeyPrefix = "account:rev:"

	// accountRevTTL  TTL ключа rev в Redis.
	// 30 дней = max token lifetime; после истечения все токены всё равно истекут, rev больше не нужен.
	accountRevTTL = 30 * 24 * time.Hour

	// l1TTL  время жизни записи в L1 in-memory кеше.
	l1TTL = 60 * time.Second

	// l1Capacity  максимальное количество записей в L1.
	l1Capacity = 10_000

	blacklistTracerName = "identy-service/adapter/redis-token-blacklist"
)

// 
// L1 кеш: запись
// 

type l1Entry struct {
	expiresAt time.Time
}

// 
// TokenBlacklistAdapter
// 

// TokenBlacklistAdapter реализует порт out.TokenBlacklist.
//
// Архитектура кеширования для jti:
//   - L1: in-process, TTL 60s. Кеширует ТОЛЬКО blacklisted=true записи.
//   - L2: Redis. jwt:black:<jti> + account:rev:<accountID>.
//
// Fail-closed: ошибка Redis в Contains/GetAccountRev возвращается вызывающему;
// TokenService интерпретирует её как ErrTokenRevoked (HTTP 401).
type TokenBlacklistAdapter struct {
	client redis.Cmdable
	logger logger.Logger
	tracer trace.Tracer

	l1mu  sync.RWMutex
	l1    map[string]l1Entry
	l1Cnt int
}

// NewTokenBlacklistAdapter создаёт адаптер.
// logger nil  используется zap.NewNop().
func NewTokenBlacklistAdapter(client redis.Cmdable, logger logger.Logger) *TokenBlacklistAdapter {
	return &TokenBlacklistAdapter{
		client: client,
		logger: logger,
		tracer: otel.Tracer(blacklistTracerName),
		l1:     make(map[string]l1Entry, l1Capacity),
	}
}

// 
// Add
// 

// Add записывает jti в Redis blacklist (L2).
// TTL = expiresAtUnix - now; если TTL <= 0  no-op (токен уже истёк).
func (a *TokenBlacklistAdapter) Add(ctx context.Context, jti string, expiresAtUnix int64) error {
	ctx, span := a.tracer.Start(ctx, "tokenBlacklist.Add",
		trace.WithAttributes(attribute.String("jti.prefix", safePrefix(jti))),
	)
	defer span.End()

	ttl := time.Duration(expiresAtUnix-time.Now().Unix()) * time.Second
	if ttl <= 0 {
		span.SetStatus(codes.Ok, "token already expired, skip")
		return nil
	}

	key := blacklistKeyPrefix + jti
	if err := a.client.Set(ctx, key, "1", ttl).Err(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		sctx := span.SpanContext()
		a.logger.Error("tokenBlacklist: Add failed",
			zap.String("error", err.Error()),
			zap.String("trace_id", sctx.TraceID().String()),
			zap.String("span_id", sctx.SpanID().String()),
		)
		return fmt.Errorf("tokenBlacklist.Add: redis SET failed: %w", err)
	}
	return nil
}

// 
// Contains
// 

// Contains проверяет наличие jti в blacklist: L1 -> L2.
//
// L1 HIT: (true, nil) без запроса в Redis.
// L1 MISS -> Redis HIT: пишем в L1, возвращаем (true, nil).
// L1 MISS -> Redis MISS: (false, nil). L1 не пополняется.
// Redis недоступен: (false, err)  fail-closed в TokenService.
func (a *TokenBlacklistAdapter) Contains(ctx context.Context, jti string) (bool, error) {
	ctx, span := a.tracer.Start(ctx, "tokenBlacklist.Contains",
		trace.WithAttributes(attribute.String("jti.prefix", safePrefix(jti))),
	)
	defer span.End()

	// L1 lookup
	a.l1mu.RLock()
	entry, hit := a.l1[jti]
	a.l1mu.RUnlock()

	if hit && time.Now().Before(entry.expiresAt) {
		span.SetAttributes(attribute.Bool("l1.hit", true))
		return true, nil
	}

	span.SetAttributes(attribute.Bool("l1.hit", false))

	key := blacklistKeyPrefix + jti
	count, err := a.client.Exists(ctx, key).Result()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		sctx := span.SpanContext()
		a.logger.Error("tokenBlacklist: Contains redis unavailable",
			zap.String("operation", "contains"),
			zap.String("fallback", "postgres"),
			zap.String("error", err.Error()),
			zap.String("trace_id", sctx.TraceID().String()),
			zap.String("span_id", sctx.SpanID().String()),
		)
		return false, fmt.Errorf("tokenBlacklist.Contains: redis unavailable: %w", err)
	}

	if count > 0 {
		span.SetAttributes(attribute.Bool("l2.hit", true))
		a.setL1(jti)
		return true, nil
	}

	span.SetAttributes(attribute.Bool("l2.hit", false))
	return false, nil
}

// 
// GetAccountRev
// 

// GetAccountRev возвращает текущий rev аккаунта из Redis L2.
// L2 miss: возвращает (0, nil)  mass-revoke не инициировался.
// Redis недоступен: (0, err)  fail-closed.
func (a *TokenBlacklistAdapter) GetAccountRev(ctx context.Context, accountID string) (int64, error) {
	ctx, span := a.tracer.Start(ctx, "tokenBlacklist.GetAccountRev",
		trace.WithAttributes(attribute.String("account.id", accountID)),
	)
	defer span.End()

	key := accountRevKeyPrefix + accountID
	val, err := a.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		sctx := span.SpanContext()
		a.logger.Error("tokenBlacklist: GetAccountRev redis unavailable",
			zap.String("operation", "get_account_rev"),
			zap.String("fallback", "postgres"),
			zap.String("account_id", accountID),
			zap.String("error", err.Error()),
			zap.String("trace_id", sctx.TraceID().String()),
			zap.String("span_id", sctx.SpanID().String()),
		)
		return 0, fmt.Errorf("tokenBlacklist.GetAccountRev: redis unavailable: %w", err)
	}

	rev, parseErr := strconv.ParseInt(val, 10, 64)
	if parseErr != nil {
		span.RecordError(parseErr)
		span.SetStatus(codes.Error, "corrupted rev value")
		sctx := span.SpanContext()
		a.logger.Error("tokenBlacklist: GetAccountRev corrupted value in Redis",
			zap.String("account_id", accountID),
			zap.String("raw_value", val),
			zap.String("trace_id", sctx.TraceID().String()),
			zap.String("span_id", sctx.SpanID().String()),
		)
		return 0, fmt.Errorf("tokenBlacklist.GetAccountRev: corrupted rev value %q in Redis: %w", val, parseErr)
	}
	return rev, nil
}

// 
// SetAccountRev
// 

// SetAccountRev записывает rev аккаунта в Redis L2.
// Вызывается после mass-revoke (LockAccount, ChangePassword, SoftDelete).
// TTL = 30 дней.
func (a *TokenBlacklistAdapter) SetAccountRev(ctx context.Context, accountID string, rev int64) error {
	ctx, span := a.tracer.Start(ctx, "tokenBlacklist.SetAccountRev",
		trace.WithAttributes(
			attribute.String("account.id", accountID),
			attribute.Int64("rev", rev),
		),
	)
	defer span.End()

	key := accountRevKeyPrefix + accountID
	if err := a.client.Set(ctx, key, strconv.FormatInt(rev, 10), accountRevTTL).Err(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		sctx := span.SpanContext()
		a.logger.Error("tokenBlacklist: SetAccountRev failed",
			zap.String("account_id", accountID),
			zap.Int64("rev", rev),
			zap.String("error", err.Error()),
			zap.String("trace_id", sctx.TraceID().String()),
			zap.String("span_id", sctx.SpanID().String()),
		)
		return fmt.Errorf("tokenBlacklist.SetAccountRev: redis SET failed: %w", err)
	}
	return nil
}

// 
// internal: L1 helpers
// 

func (a *TokenBlacklistAdapter) setL1(jti string) {
	a.l1mu.Lock()
	defer a.l1mu.Unlock()

	if a.l1Cnt >= l1Capacity {
		a.evictOneExpired()
	}
	if a.l1Cnt >= l1Capacity {
		return
	}
	_, existed := a.l1[jti]
	a.l1[jti] = l1Entry{expiresAt: time.Now().Add(l1TTL)}
	if !existed {
		a.l1Cnt++
	}
}

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

// safePrefix возвращает первые 8 символов jti для trace attributes (PII mitigation).
func safePrefix(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// 
// Compile-time interface assertion
// 

var _ interface {
	Add(ctx context.Context, jti string, expiresAtUnix int64) error
	Contains(ctx context.Context, jti string) (bool, error)
	GetAccountRev(ctx context.Context, accountID string) (int64, error)
	SetAccountRev(ctx context.Context, accountID string, rev int64) error
} = (*TokenBlacklistAdapter)(nil)
