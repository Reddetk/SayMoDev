// Package di собирает зависимости процесса. Контейнер один, глобальных синглтонов нет.
// goca:manual — goca di генерирует Wire-каркас без Kafka/S3-проб BC#3.
package di

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/adapter/kafka"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/adapter/postgres"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/adapter/redis"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/adapter/s3"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/auth"
	handlerhttp "github.com/Reddetk/SayMoDev/rehabilitation-service/internal/handler/http"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/health"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/platform/metrics"
	repositorypostgres "github.com/Reddetk/SayMoDev/rehabilitation-service/internal/repository/postgres"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/usecase"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/pkg/config"
	"go.uber.org/zap"
)

// App — собранный процесс: HTTP, metrics и адаптеры.
type App struct {
	cfg        *config.Config
	log        *zap.Logger
	metrics    *metrics.Metrics
	httpServer *http.Server
	metServer  *http.Server
	postgres   *postgres.Probe
	redis      *redis.Probe
	redisBlack *redis.Probe
}

// New собирает приложение. Ошибка возвращается только при невалидной конфигурации
// или невозможности создать клиентов; недоступность зависимостей видна в /ready.
func New(ctx context.Context, cfg *config.Config, build handlerhttp.BuildInfo) (*App, error) {
	log, err := newLogger(cfg)
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

	repo := repositorypostgres.NewProgramRepository(pg.Pool())
	presigner := s3.NewPresigner(s3probe.Client(), cfg.S3Bucket)
	query := usecase.NewProgramQuery(repo, presigner, redis.NewCache(rdb.Client()), log)
	content := &handlerhttp.ContentHandler{Query: query, Log: log}

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
		cfg:        cfg,
		log:        log,
		metrics:    met,
		postgres:   pg,
		redis:      rdb,
		redisBlack: redisBlack,
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
	_ = a.log.Sync()
}
