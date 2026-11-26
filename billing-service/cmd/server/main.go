// Package main — точка входа billing-service (BC#2).
//
// Реализует 12 операций BC#2, outbox publisher, consumer BC#1 и lifecycle worker.
// Два HTTP-сервера:
//   - API :8002            — бизнес-ручки, /health*, /swagger;
//   - admin :9002          — /metrics и /health/live.
//
// goca:manual — точка входа заменяет сгенерированный goca main.go (два порта, health, graceful shutdown).
//
// @title						SayMo Billing Service API
// @version					1.0
// @description				BC#2 Billing and Subscriptions Management (SayMo).
// @BasePath					/
// @schemes					http https
// @securityDefinitions.apikey	BearerAuth
// @in							header
// @name						Authorization
// @description				JWT access token. Формат: Bearer {token}.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	_ "github.com/Reddetk/SayMoDev/billing-service/docs"

	"github.com/Reddetk/SayMoDev/billing-service/internal/adapter/acquiring"
	"github.com/Reddetk/SayMoDev/billing-service/internal/adapter/auth"
	kafkaadapter "github.com/Reddetk/SayMoDev/billing-service/internal/adapter/kafka"
	"github.com/Reddetk/SayMoDev/billing-service/internal/adapter/postbox"
	redisadapter "github.com/Reddetk/SayMoDev/billing-service/internal/adapter/redis"
	httpapi "github.com/Reddetk/SayMoDev/billing-service/internal/handler/http"
	"github.com/Reddetk/SayMoDev/billing-service/internal/platform/config"
	"github.com/Reddetk/SayMoDev/billing-service/internal/platform/health"
	"github.com/Reddetk/SayMoDev/billing-service/internal/platform/metrics"
	"github.com/Reddetk/SayMoDev/billing-service/internal/platform/telemetry"
	"github.com/Reddetk/SayMoDev/billing-service/internal/repository/postgres"
	"github.com/Reddetk/SayMoDev/billing-service/internal/usecase"
)

const serviceName = "billing-service"

var (
	// Version и BuildTime подставляются -ldflags при сборке.
	Version   = "dev"
	BuildTime = "unknown"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: fatal: %v\n", serviceName, err)
		os.Exit(1)
	}
}

func run() error {
	if err := config.LoadEnvFiles(); err != nil {
		return err
	}
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return err
	}

	logger, err := newLogger(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = logger.Sync() }()

	logger.Info("starting",
		zap.String("service", serviceName),
		zap.String("env", cfg.Env),
		zap.String("version", Version),
		zap.String("build_time", BuildTime),
		zap.Int("api_port", cfg.Ports.Server),
		zap.Int("metrics_port", cfg.Ports.Metrics),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := telemetry.Init(ctx, serviceName, Version, cfg.OTELEndpoint)
	if err != nil {
		return fmt.Errorf("telemetry: %w", err)
	}
	defer func() { _ = shutdownTracing(context.Background()) }()

	if cfg.Env == config.EnvProd {
		gin.SetMode(gin.ReleaseMode)
	}

	pool, err := pgxpool.New(ctx, cfg.Postgres.DSN)
	if err != nil {
		return fmt.Errorf("postgres pool: %w", config.RedactError(err, cfg.Postgres.DSN))
	}
	defer pool.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
	})
	defer func() { _ = redisClient.Close() }()

	blacklistClient := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.BlacklistAddr,
		Password: cfg.Redis.BlacklistPass,
	})
	defer func() { _ = blacklistClient.Close() }()

	store := postgres.New(pool)
	cache := redisadapter.New(redisClient, blacklistClient)
	registry := metrics.NewRegistry(Version, cfg.Env)
	metrics.NewSubscriptionGauge(registry, func() []metrics.SubscriptionCount {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		counts, err := store.SubscriptionCounts(ctx)
		if err != nil {
			return nil
		}
		result := make([]metrics.SubscriptionCount, 0, len(counts))
		for _, item := range counts {
			result = append(result, metrics.SubscriptionCount{Status: item.Status, PlanType: item.PlanType, Count: item.Count})
		}
		return result
	})
	metrics.NewOutboxPendingGauge(registry, func() (float64, bool) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		count, err := store.PendingOutboxCount(ctx)
		if err != nil {
			return 0, false
		}
		return float64(count), true
	})
	verifier, err := auth.NewVerifier(cfg.JWT.Issuer, cfg.JWT.Audience, cfg.JWT.JWKSURL, cfg.JWT.PublicKeyPath, auth.NewHTTPClient())
	if err != nil {
		return fmt.Errorf("jwt verifier: %w", err)
	}

	notifyURL := cfg.Acquiring.NotifyURL
	if notifyURL == "" {
		notifyURL = fmt.Sprintf("http://localhost:%d/billing/webhooks/acquiring", cfg.Ports.Server)
	}
	acquiringAdapter := acquiring.New(cfg.Acquiring.APIURL, cfg.Acquiring.StoreID, cfg.Acquiring.SecretKey, cfg.Acquiring.ReturnURL).
		WithMetrics(metrics.NewAcquiring(registry))
	postboxAdapter, err := postbox.New(postbox.Config{
		Endpoint:    cfg.Postbox.Endpoint,
		IAMToken:    cfg.Postbox.Token,
		AccessKey:   cfg.Postbox.AccessKey,
		SecretKey:   cfg.Postbox.SecretKey,
		Region:      cfg.Postbox.Region,
		FromAddress: cfg.Postbox.FromAddress,
	})
	if err != nil {
		return fmt.Errorf("postbox: %w", err)
	}
	postboxAdapter.WithMetrics(metrics.NewPostbox(registry))
	service := usecase.New(store, cache, acquiringAdapter, postboxAdapter, usecase.Config{
		StoreID:               cfg.Acquiring.StoreID,
		NotifyURL:             notifyURL,
		ReturnURL:             cfg.Acquiring.ReturnURL,
		WebhookSecret:         cfg.Acquiring.SecretKey,
		NotificationRecipient: cfg.Postbox.DefaultRecipient,
	})

	healthService := health.New(Version, cfg.HealthTimeout,
		health.Check{
			Name: "postgres",
			Checker: health.CheckerFunc(func(ctx context.Context) error {
				return pool.Ping(ctx)
			}),
		},
		health.Check{
			Name: "redis",
			Checker: health.CheckerFunc(func(ctx context.Context) error {
				return redisClient.Ping(ctx).Err()
			}),
		},
		health.Check{
			Name: "kafka",
			Checker: health.CheckerFunc(func(ctx context.Context) error {
				return pingKafka(ctx, cfg.Kafka.Brokers, cfg.HealthTimeout)
			}),
		},
	)

	businessMetrics := metrics.NewBusiness(registry)
	service.SetMetrics(businessMetrics)

	healthHandler := httpapi.NewHealthHandler(healthService)
	billingHandler := httpapi.NewBillingHandler(service)
	authMiddleware := httpapi.AuthMiddleware(verifier, store, cache)
	idempotency := func(required bool) gin.HandlerFunc {
		return httpapi.IdempotencyMiddleware(store, cache, required)
	}
	apiRouter := httpapi.NewFullRouter(healthHandler, billingHandler, authMiddleware, idempotency, cfg.CORSOrigins, 1<<20)
	apiRouter.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	adminMux := httpapi.NewAdminMux(
		metrics.Handler(registry),
		healthHandler.LiveHandler(),
	)

	apiServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Ports.Server),
		Handler:           apiRouter,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	adminServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Ports.Metrics),
		Handler:           adminMux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 2)
	serve := func(name string, server *http.Server) {
		logger.Info("http server listening", zap.String("server", name), zap.String("addr", server.Addr))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("%s: %w", name, err)
		}
	}
	go serve("api", apiServer)
	go serve("admin", adminServer)

	slogLogger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	publisher := kafkaadapter.NewPublisher(store, cfg.Kafka.Brokers, cfg.Kafka.EventsTopic, slogLogger, metrics.NewOutbox(registry))
	go publisher.Run(ctx)
	consumer := kafkaadapter.NewConsumer(cfg.Kafka.Brokers, cfg.Kafka.IdentityTopic, cfg.Kafka.ConsumerGroup, identityHandler{service: service}, slogLogger)
	go consumer.Run(ctx)
	go service.RunLifecycle(ctx, cfg.LifecycleInterval, slogLogger)

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	var shutdownErr error
	for _, server := range []*http.Server{apiServer, adminServer} {
		if err := server.Shutdown(shutdownCtx); err != nil {
			shutdownErr = errors.Join(shutdownErr, err)
		}
	}

	logger.Info("stopped")
	return shutdownErr
}

// identityHandler адаптирует Kafka-событие к application-слою.
type identityHandler struct {
	service *usecase.Service
}

func (h identityHandler) HandleIdentityEvent(ctx context.Context, event kafkaadapter.IdentityEvent) error {
	return h.service.HandleIdentityEvent(ctx, usecase.IdentityEvent{
		EventID:   event.EventID,
		EventType: event.EventType,
		Payload:   event.Payload,
	})
}

func pingKafka(ctx context.Context, brokers []string, timeout time.Duration) error {
	var lastErr error
	for _, broker := range brokers {
		dialer := &kafka.Dialer{Timeout: timeout, DualStack: true}
		conn, err := dialer.DialContext(ctx, "tcp", broker)
		if err != nil {
			lastErr = err
			continue
		}
		if err := conn.Close(); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("no kafka brokers configured")
	}
	return fmt.Errorf("kafka unreachable: %w", lastErr)
}

func newLogger(cfg *config.Config) (*zap.Logger, error) {
	level := zap.NewAtomicLevelAt(zapcore.InfoLevel)
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		return nil, fmt.Errorf("log level %q: %w", cfg.LogLevel, err)
	}

	zapCfg := zap.NewProductionConfig()
	zapCfg.Level = level
	if cfg.Env != config.EnvProd {
		zapCfg.Development = true
		zapCfg.Encoding = "console"
	}
	if cfg.LogFormat == "console" && cfg.Env != config.EnvProd {
		zapCfg.Encoding = "console"
	}
	zapCfg.EncoderConfig.TimeKey = "ts"
	zapCfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	logger, err := zapCfg.Build()
	if err != nil {
		return nil, fmt.Errorf("logger: %w", err)
	}
	return logger.Named(serviceName), nil
}
