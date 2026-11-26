// @title           progress-service API (BC#4)
// @version         1.0
// @description     Progress tracking and assessment: programs, answers, completion, tariff-based access.
// @BasePath        /
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// goca:manual вЂ” the enterprise template generates gorilla/mux + GORM/sqlite; BC#4 requires
// gin + pgx + port-owned DTOs (docs, ADR-007), so the composition root is hand-wired.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	httpapi "github.com/Reddetk/SayMoDev/progress-service/internal/adapter/primary/http"
	"github.com/Reddetk/SayMoDev/progress-service/internal/adapter/secondary/content"
	"github.com/Reddetk/SayMoDev/progress-service/internal/adapter/secondary/jwks"
	"github.com/Reddetk/SayMoDev/progress-service/internal/adapter/secondary/kafka"
	"github.com/Reddetk/SayMoDev/progress-service/internal/adapter/secondary/postgres"
	redisadapter "github.com/Reddetk/SayMoDev/progress-service/internal/adapter/secondary/redis"
	s3adapter "github.com/Reddetk/SayMoDev/progress-service/internal/adapter/secondary/s3"
	"github.com/Reddetk/SayMoDev/progress-service/internal/adapter/telemetry"
	"github.com/Reddetk/SayMoDev/progress-service/internal/config"
	"github.com/Reddetk/SayMoDev/progress-service/internal/core/service"
	"github.com/Reddetk/SayMoDev/progress-service/internal/logger"

	_ "github.com/Reddetk/SayMoDev/progress-service/docs"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	zapLog, err := logger.New(cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		log.Fatalf("logger: %v", err)
	}
	defer func() { _ = zapLog.Sync() }()
	logg := logger.Logger(zapLog)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdownTracer, err := telemetry.InitTracer(ctx, cfg.OTELEnabled, cfg.OTELEndpoint, cfg.ServiceName)
	if err != nil {
		logg.Warn("otel_init_failed", zap.Error(err))
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTracer(shutdownCtx)
	}()

	repo := connectPostgres(ctx, cfg, logg)
	defer repo.Close()

	cacheClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword, DB: cfg.RedisDB})
	defer cacheClient.Close()
	blacklistClient := redis.NewClient(&redis.Options{Addr: cfg.BlacklistAddr, Password: cfg.BlacklistPassword, DB: cfg.BlacklistDB})
	defer blacklistClient.Close()

	cache := redisadapter.NewCache(cacheClient, cfg.LocalCacheTTL)
	idem := redisadapter.NewIdempotency(cacheClient)
	revocation := redisadapter.NewRevocation(blacklistClient)

	jwksProvider := jwks.New(cfg.JWKSURL, cfg.JWKSRefreshTTL, 2*time.Second)
	tokenService := service.NewTokenService(jwksProvider, revocation, cfg.JWTIssuer, cfg.JWTAudience)

	catalog := content.New(cfg.BC3BaseURL, content.Options{
		Timeout: cfg.BC3Timeout, RetryDelay: cfg.BC3RetryDelay,
		FailureThreshold: cfg.BC3CircuitFailures, OpenDuration: cfg.BC3CircuitOpen, HalfOpenMax: cfg.BC3CircuitHalfOpenMax,
	})

	storage, err := s3adapter.New(ctx, s3adapter.Options{
		Endpoint: cfg.S3Endpoint, Region: cfg.S3Region, Bucket: cfg.S3Bucket,
		AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, ForcePathStyle: cfg.S3ForcePathStyle,
	})
	if err != nil {
		logg.Fatal("s3_init_failed", zap.Error(err))
	}

	progressSvc := service.NewProgressService(repo, repo, catalog, cache, logg, cfg.CacheTTL, cfg.DefaultWeekBitmap)
	lessonSvc := service.NewLessonService(repo, repo, catalog, storage, cache, logg, cfg.CacheTTL, cfg.S3PresignPutTTL)
	adminSvc := service.NewAdminService(repo, repo, catalog, storage, progressSvc, logg, cfg.S3PresignGetTTL)
	eventSvc := service.NewEventService(repo, repo, catalog, cache, logg, cfg.CacheTTL, cfg.DefaultWeekBitmap)

	var producer *kafka.Producer
	if cfg.KafkaEnabled() {
		if err := kafka.EnsureTopics(cfg.KafkaBrokers, cfg.IdentityTopic, cfg.BillingTopic, cfg.ContentTopic, cfg.ProgressTopic, cfg.DeadLetterTopic); err != nil {
			logg.Fatal("kafka_topics_failed", zap.Error(err))
		}
		producer = kafka.NewProducer(cfg.KafkaBrokers)
		defer func() { _ = producer.Close() }()

		worker := kafka.NewOutboxWorker(repo, producer, cfg.ProgressTopic, cfg.OutboxBatchSize, cfg.OutboxPollEvery, logg)
		go worker.Run(ctx)

		consumer := kafka.NewConsumer(cfg.KafkaBrokers, cfg.KafkaGroupID,
			[]string{cfg.IdentityTopic, cfg.BillingTopic, cfg.ContentTopic},
			eventSvc, producer, cfg.DeadLetterTopic, logg)
		defer func() { _ = consumer.Close() }()
		go consumer.Run(ctx)
		logg.Info("kafka_enabled", zap.Strings("brokers", cfg.KafkaBrokers))
	} else {
		logg.Warn("kafka_disabled", zap.String("reason", "KAFKA_BROKERS is empty"))
	}

	handler := httpapi.NewRouter(httpapi.Deps{
		Config: cfg, Log: logg, Validator: tokenService, Idem: idem,
		Progress: progressSvc, Programs: progressSvc, Answers: lessonSvc, Completer: lessonSvc,
		Deleter: progressSvc, Admin: adminSvc, AdminOp: adminSvc, Guard: adminSvc,
		Health: httpapi.HealthDeps{Postgres: repo, Cache: cache, Blacklist: revocation},
	})

	go updateActiveProgramsGauge(ctx, repo, logg)

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		logg.Info("server_started", zap.String("addr", cfg.HTTPAddr), zap.String("env", cfg.Environment))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logg.Fatal("server_failed", zap.Error(err))
		}
	}()

	<-ctx.Done()
	logg.Info("shutdown_started")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logg.Warn("server_shutdown_failed", zap.Error(err))
	}
	logg.Info("shutdown_complete")
}

func connectPostgres(ctx context.Context, cfg *config.Config, logg logger.Logger) *postgres.Repo {
	var lastErr error
	for attempt := 1; attempt <= 10; attempt++ {
		repo, err := postgres.New(ctx, cfg.PostgresDSN, cfg.PostgresMaxConns, cfg.PostgresMinConns, cfg.PostgresMaxLifetime)
		if err == nil {
			logg.Info("postgres_connected")
			return repo
		}
		lastErr = err
		logg.Warn("postgres_connect_retry", zap.Int("attempt", attempt), zap.Error(err))
		select {
		case <-ctx.Done():
			logg.Fatal("postgres_connect_cancelled", zap.Error(lastErr))
		case <-time.After(time.Duration(attempt) * time.Second):
		}
	}
	logg.Fatal("postgres_connect_failed", zap.Error(lastErr))
	return nil
}

// updateActiveProgramsGauge refreshes active_programs_count every 30s (Observability.md Business Gauge).
func updateActiveProgramsGauge(ctx context.Context, repo *postgres.Repo, logg logger.Logger) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if n, err := repo.ActiveProgramCount(ctx); err == nil {
			telemetry.ActiveProgramsGauge.Set(float64(n))
		} else if ctx.Err() == nil {
			logg.Warn("active_programs_gauge_failed", zap.Error(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
