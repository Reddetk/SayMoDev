// Package di собирает зависимости процесса. Контейнер один, глобальных синглтонов нет.
// goca:manual — goca di генерирует Wire-каркас без Kafka/S3-проб BC#3.
package di

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/adapter/bc4"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/adapter/kafka"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/adapter/postgres"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/adapter/redis"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/adapter/s3"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/auth"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/consumer"
	handlerhttp "github.com/Reddetk/SayMoDev/rehabilitation-service/internal/handler/http"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/health"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/outbox"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/platform/metrics"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/platform/telemetry"
	repositorypostgres "github.com/Reddetk/SayMoDev/rehabilitation-service/internal/repository/postgres"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/usecase"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/pkg/config"
	"go.uber.org/zap"
)

// App — собранный процесс: HTTP, metrics и адаптеры.
type App struct {
	cfg             *config.Config
	log             *zap.Logger
	metrics         *metrics.Metrics
	httpServer      *http.Server
	metServer       *http.Server
	postgres        *postgres.Probe
	redis           *redis.Probe
	redisBlack      *redis.Probe
	outbox          *outbox.Worker
	consumer        *consumer.Worker
	producer        *kafka.Producer
	invalidation    *redis.Invalidator
	query           *usecase.ProgramQuery
	shutdownTracing func(context.Context) error
}

// New собирает приложение. Ошибка возвращается только при невалидной конфигурации
// или невозможности создать клиентов; недоступность зависимостей видна в /ready.
func New(ctx context.Context, cfg *config.Config, build handlerhttp.BuildInfo) (*App, error) {
	log, err := newLogger(cfg)
	if err != nil {
		return nil, err
	}

	shutdownTracing, err := telemetry.Init(ctx, cfg.JaegerOTLPEndpoint, "rehabilitation-service", build.Version)
	if err != nil {
		return nil, err
	}

	pg, err := postgres.New(ctx, cfg.PostgresDSN)
	if err != nil {
		return nil, err
	}

	rdb := redis.New(cfg.RedisAddr, cfg.RedisPassword)
	kf := kafka.New(cfg.KafkaBrokers, cfg.ProbeTimeout)

	s3probe, err := s3.New(ctx, s3.Options{
		Endpoint:  cfg.S3Endpoint,
		Region:    cfg.S3Region,
		Bucket:    cfg.S3Bucket,
		AccessKey: cfg.S3AccessKey,
		SecretKey: cfg.S3SecretKey,
	})
	if err != nil {
		pg.Close()
		_ = rdb.Close()
		return nil, err
	}

	checker := health.NewChecker(cfg.ProbeTimeout, pg, rdb, kf, s3probe)
	met := metrics.New(build.Version, build.Commit)

	var redisBlack *redis.Probe
	if cfg.RedisBlacklistAddr != "" {
		redisBlack = redis.New(cfg.RedisBlacklistAddr, cfg.RedisBlacklistPass)
	}

	// S3: JWT валидируется только если задан JWKS и есть shared blacklist.
	// Если зависимостей нет, health/readiness работают, а защищённые группы
	// отвечают 503 (denyProtected) — публичными они не становятся.
	var authn *auth.Authenticator
	if cfg.JWKSURL != "" && redisBlack != nil {
		authn = auth.NewAuthenticator(
			auth.Options{JWKSURL: cfg.JWKSURL, Logger: log},
			auth.NewRedisRevocation(redisBlack.Client()),
		)
	}

	var bc4Guard *bc4.Client
	if cfg.BC4URL != "" {
		bc4Guard = bc4.New(cfg.BC4URL, cfg.BC4ServiceToken)
	}

	repo := repositorypostgres.NewProgramRepository(pg.Pool())
	presigner := s3.NewPresigner(s3probe.Client(), cfg.S3Bucket)
	uploader := s3.NewUploader(s3probe.Client(), cfg.S3Bucket, cfg.S3Endpoint)
	uploader.SetMetrics(met)
	query := usecase.NewProgramQuery(repo, presigner, redis.NewCache(rdb.Client()), log)
	query.SetMetrics(met)
	invalidator := redis.NewInvalidator(rdb.Client())
	query.SetInvalidationPublisher(invalidator)
	commands := usecase.NewProgramCommand(repo, uploader, query)
	if bc4Guard != nil {
		commands = usecase.NewProgramCommand(repo, uploader, query, bc4Guard)
	}
	content := &handlerhttp.ContentHandler{Query: query, Commands: commands, Metrics: met, Log: log}

	// Гauge-снимки из БД: иначе после рестарта programs_count/active_reference_count_total врали бы.
	if counts, err := repo.ProgramCounts(ctx); err != nil {
		log.Warn("initial programs_count failed", zap.Error(err))
	} else {
		for key, count := range counts {
			parts := strings.SplitN(key, "|", 2)
			if len(parts) == 2 {
				met.ProgramsCount.WithLabelValues(parts[0], parts[1], "true").Set(float64(count))
			}
		}
	}
	if total, err := repo.ActiveReferenceTotal(ctx); err != nil {
		log.Warn("initial active_reference_count_total failed", zap.Error(err))
	} else {
		met.ActiveReferenceCount.Set(float64(total))
	}
	if lessons, err := repo.LessonCounts(ctx); err != nil {
		log.Warn("initial lessons_per_program failed", zap.Error(err))
	} else {
		for programID, count := range lessons {
			met.LessonsPerProgram.WithLabelValues(programID).Set(float64(count))
		}
	}

	producer := kafka.NewProducer(cfg.KafkaBrokers)
	outboxWorker := outbox.NewWorker(repositorypostgres.NewOutboxStore(pg.Pool()), producer, "saymo.content.events", log)
	consumerWorker := consumer.NewWorker(
		kafka.NewConsumer(cfg.KafkaBrokers, "bc3-content", "saymo.progress.events"),
		repositorypostgres.NewProgramEventStore(pg.Pool()),
		log,
	)
	consumerWorker.SetMetrics(met)

	router := handlerhttp.NewRouter(handlerhttp.Deps{
		Health:        checker,
		Metrics:       met,
		Logger:        log,
		Build:         build,
		CORSOrigins:   cfg.CORSAllowedOrigins,
		EnableSwagger: cfg.Env != config.EnvProd,
		Auth:          authn,
		Content:       content,
	})

	app := &App{
		cfg:             cfg,
		log:             log,
		metrics:         met,
		postgres:        pg,
		redis:           rdb,
		redisBlack:      redisBlack,
		outbox:          outboxWorker,
		consumer:        consumerWorker,
		producer:        producer,
		invalidation:    invalidator,
		query:           query,
		shutdownTracing: shutdownTracing,
		httpServer: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           router,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      10 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
		metServer: &http.Server{
			Addr:              cfg.MetricsAddr,
			Handler:           met.Handler(),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      10 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
	}

	return app, nil
}

// Run запускает оба сервера и корректно останавливает их по отмене ctx.
func (a *App) Run(ctx context.Context) error {
	a.log.Info("server starting",
		zap.String("env", string(a.cfg.Env)),
		zap.String("http_addr", a.cfg.HTTPAddr),
		zap.String("metrics_addr", a.cfg.MetricsAddr),
	)

	errCh := make(chan error, 2)
	go a.outbox.Run(ctx)
	go a.consumer.Run(ctx)
	go a.invalidation.Run(ctx, a.query.ApplyInvalidation)
	go func() {
		if err := a.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	go func() {
		if err := a.metServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errCh:
	}

	a.metrics.ServiceUp.Set(0)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownTimeout)
	defer cancel()

	_ = a.httpServer.Shutdown(shutdownCtx)
	_ = a.metServer.Shutdown(shutdownCtx)
	a.close()

	if runErr != nil {
		a.log.Error("server stopped with error", zap.Error(runErr))
	}
	return runErr
}

func (a *App) close() {
	a.postgres.Close()
	if a.redisBlack != nil {
		_ = a.redisBlack.Close()
	}
	_ = a.redis.Close()
	_ = a.producer.Close()
	if a.shutdownTracing != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = a.shutdownTracing(shutdownCtx)
		cancel()
	}
	_ = a.log.Sync()
}
