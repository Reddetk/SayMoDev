// Package main -- точка запуска identity-service (BC#1).
//
// Порядок инициализации:
//  1. Logger (zap, JSON)
//  2. PostgreSQL pool (pgxpool)
//  3. Redis client
//  4. OTel tracing
//  5. Adapters (secondary)
//  6. Core services
//  7. HTTP router (primary adapter)
//  8. Graceful shutdown
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"

	primary "github.com/Reddetk/SayMoDev/identy-service/adapter/primary/http"
	secondary "github.com/Reddetk/SayMoDev/identy-service/adapter/secondary"
	redisada "github.com/Reddetk/SayMoDev/identy-service/adapter/secondary/redis"
	"github.com/Reddetk/SayMoDev/identy-service/core"
)

const (
	serviceName     = "identity-service"
	shutdownTimeout = 15 * time.Second
)

func main() {
	logger, err := zap.NewProduction()
	if err != nil {
		// zap.NewProduction не должен падать в нормальных условиях.
		panic(fmt.Sprintf("failed to init logger: %v", err))
	}
	defer func() { _ = logger.Sync() }()

	if err := run(logger); err != nil {
		logger.Error("identity-service: fatal startup error", zap.Error(err))
		os.Exit(1)
	}
}

func run(logger *zap.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// --- 1. OTel tracing -------------------------------------------------------
	// Спецификация: Observability.md -- OTLP gRPC экспортёр.
	shutdownTracer, err := initTracer(ctx)
	if err != nil {
		// Non-fatal: observability не должна блокировать запуск сервиса.
		logger.Warn("otel tracer init failed, continuing without tracing", zap.Error(err))
	} else {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := shutdownTracer(shutdownCtx); err != nil {
				logger.Warn("otel tracer shutdown error", zap.Error(err))
			}
		}()
	}

	// --- 2. PostgreSQL pool ----------------------------------------------------
	// BC#1 invariant: PC/EC -- consistency over availability.
	// Pool закрывается в defer после HTTP-сервера.
	dsn := requireEnv("POSTGRES_DSN")
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("pgxpool.New: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("postgres ping: %w", err)
	}
	logger.Info("postgres connected")

	// --- 3. Redis client -------------------------------------------------------
	// L2 кэш: jti blacklist, account:rev, rate limit counters.
	// Спецификация: fail-closed при недоступности rate limiting (§5 Rate Limiting Invariant).
	redisOpt, err := redis.ParseURL(requireEnv("REDIS_URL"))
	if err != nil {
		return fmt.Errorf("redis.ParseURL: %w", err)
	}
	redisClient := redis.NewClient(redisOpt)
	defer func() {
		if err := redisClient.Close(); err != nil {
			logger.Warn("redis close error", zap.Error(err))
		}
	}()

	if err := redisClient.Ping(ctx).Err(); err != nil {
		// fail-closed: Redis недоступен при старте -- не запускаем сервис.
		// Причина: rate limiting и blacklist деградируют без Redis (§5, §Caching Strategy).
		return fmt.Errorf("redis ping: %w", err)
	}
	logger.Info("redis connected")

	// --- 4. Secondary adapters -------------------------------------------------
	repo, err := secondary.NewPostgresAccountRepository(pool, logger)
	if err != nil {
		return fmt.Errorf("NewPostgresAccountRepository: %w", err)
	}

	tokenIssuer := secondary.NewRSATokenIssuer( // TODO error fall
		requireEnv("JWT_PRIVATE_KEY_PATH"),
		requireEnv("JWT_KID"),
		logger,
	)
	if err != nil {
		return fmt.Errorf("NewRSATokenIssuer: %w", err)
	}

	blacklist := redisada.NewTokenBlacklistAdapter(redisClient, logger) // TODO error fall
	if err != nil {
		return fmt.Errorf("NewRedisTokenBlacklist: %w", err)
	}

	rateLimiter := redisada.NewRateLimiterAdapter(redisClient, logger) // TODO error fall
	if err != nil {
		return fmt.Errorf("NewRedisRateLimiter: %w", err)
	}

	eventsProducer, err := secondary.NewOutboxEventsProducer(pool, logger)
	if err != nil {
		return fmt.Errorf("NewOutboxEventsProducer: %w", err)
	}

	googleOAuth, err := secondary.NewGoogleOAuthAdapter(
		requireEnv("GOOGLE_CLIENT_ID"),
		requireEnv("GOOGLE_CLIENT_SECRET"),
		requireEnv("GOOGLE_REDIRECT_URI"),
		logger,
	)
	if err != nil {
		return fmt.Errorf("NewGoogleOAuthProvider: %w", err)
	}

	// --- 5. Core services ------------------------------------------------------
	authService := core.NewAuthService(
		repo,
		tokenIssuer,
		blacklist,
		rateLimiter,
		eventsProducer,
		googleOAuth,
	)

	// --- 6. Primary adapter (HTTP) ---------------------------------------------
	// CORS invariant (§8): CORS middleware выполняется ДО JWT-валидации.
	// Допустимые origins определяются через APP_ENV.
	router := primary.NewGinRouter(primary.RouterConfig{
		AuthService: authService,
		Logger:      logger,
		AppEnv:      getEnv("APP_ENV", "development"),
	})

	addr := getEnv("HTTP_ADDR", ":8080")
	server := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// --- 7. Graceful shutdown --------------------------------------------------
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("identity-service starting", zap.String("addr", addr))
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http server shutdown: %w", err)
	}

	logger.Info("identity-service stopped gracefully")
	return nil
}

// initTracer инициализирует OTel SDK с OTLP gRPC экспортёром.
// Согласно Observability.md: service.name = "identity-service".
func initTracer(ctx context.Context) (func(context.Context) error, error) {
	endpoint := getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otlptracegrpc.New: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("resource.New: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}

// requireEnv читает переменную окружения или завершает запуск с ошибкой.
func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		// Паника здесь намеренна: отсутствие обязательной переменной --
		// ошибка конфигурации, не runtime-ошибка.
		panic(fmt.Sprintf("required env variable %q is not set", key))
	}
	return v
}

// getEnv читает переменную окружения с fallback-значением.
func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
