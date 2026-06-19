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
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
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
	"github.com/Reddetk/SayMoDev/identy-service/adapter/secondary/postgres"
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
	shutdownTracer, err := initTracer(ctx)
	if err != nil {
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
		return fmt.Errorf("redis ping: %w", err)
	}
	logger.Info("redis connected")

	// --- 4. Secondary adapters -------------------------------------------------

	otpRep := postgres.NewPostgresOtpRepository(pool, logger)

	// 4a. Postgres account repository.
	repo, err := secondary.NewPostgresAccountRepository(pool, logger)
	if err != nil {
		return fmt.Errorf("NewPostgresAccountRepository: %w", err)
	}

	// 4b. RSA token issuer.
	currentPriv, err := loadRSAPrivateKey(requireEnv("JWT_PRIVATE_KEY_PATH"))
	if err != nil {
		return fmt.Errorf("load JWT private key: %w", err)
	}
	currentPub, err := loadRSAPublicKey(requireEnv("JWT_PUBLIC_KEY_PATH"))
	if err != nil {
		return fmt.Errorf("load JWT public key: %w", err)
	}
	currentKid := requireEnv("JWT_KID")

	var prevPub *rsa.PublicKey
	var prevKid string
	if prevPath := getEnv("JWT_PREV_PUBLIC_KEY_PATH", ""); prevPath != "" {
		prevPub, err = loadRSAPublicKey(prevPath)
		if err != nil {
			return fmt.Errorf("load JWT prev public key: %w", err)
		}
		prevKid = requireEnv("JWT_PREV_KID")
	}

	tokenIssuer := secondary.NewRSATokenIssuer(currentPriv, currentPub, currentKid, prevPub, prevKid)

	// 4c. Redis adapters.
	blacklist := redisada.NewTokenBlacklistAdapter(redisClient, logger)
	rateLimiter := redisada.NewRateLimiterAdapter(redisClient, logger)

	// 4d. Outbox events producer.
	eventsProducer, err := secondary.NewOutboxEventsProducer(pool, logger)
	if err != nil {
		return fmt.Errorf("NewOutboxEventsProducer: %w", err)
	}

	// 4e. Google OAuth adapter.
	googleOAuth, err := secondary.NewGoogleOAuthAdapter(secondary.GoogleOAuthConfig{
		ClientID:     requireEnv("GOOGLE_CLIENT_ID"),
		ClientSecret: requireEnv("GOOGLE_CLIENT_SECRET"),
		RedirectURI:  requireEnv("GOOGLE_REDIRECT_URI"),
		Logger:       logger,
	})
	if err != nil {
		return fmt.Errorf("NewGoogleOAuthAdapter: %w", err)
	}

	// 4f. Postbox email adapter.
	// POSTBOX_ENDPOINT: если не задан -- используется prod URL.
	// Для локальной разработки: POSTBOX_ENDPOINT=http://localhost:9025/v2/email/outbound-emails
	emailBox, err := secondary.NewPostboxEmailAdapter(secondary.PostboxConfig{
		IAMToken:    requireEnv("POSTBOX_IAM_TOKEN"),
		FromAddress: requireEnv("POSTBOX_FROM_ADDRESS"),
		Endpoint:    getEnv("POSTBOX_ENDPOINT", ""),
		Logger:      logger,
	})
	if err != nil {
		return fmt.Errorf("NewPostboxEmailAdapter: %w", err)
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

	accService := core.NewAccountService(otpRep, repo, eventsProducer, blacklist)
	tokenService := core.NewTokenService(tokenIssuer, blacklist, eventsProducer)
	sessionService := core.NewSessionService(repo, blacklist, eventsProducer)
	otpService := core.NewOTPService(otpRep, repo, emailBox)

	// --- 6. Primary adapter (HTTP) ---------------------------------------------
	router := primary.NewGinRouter(primary.RouterDeps{
		Logger:           logger,
		TokenValidator:   tokenService,
		Authenticator:    authService,
		Registrator:      accService,
		SessionOperator:  sessionService,
		TokenOperator:    tokenService,
		AccountOpertator: accService,
		PasswordOperator: accService,
		OTPIssuer:        otpService,
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

// ---------------------------------------------------------------------------
// RSA key loaders
// ---------------------------------------------------------------------------

func loadRSAPrivateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key file: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in private key file")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("PKCS8 key is not RSA")
		}
		return rsaKey, nil
	default:
		return nil, fmt.Errorf("unsupported PEM type: %s", block.Type)
	}
}

func loadRSAPublicKey(path string) (*rsa.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read public key file: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in public key file")
	}
	switch block.Type {
	case "RSA PUBLIC KEY":
		return x509.ParsePKCS1PublicKey(block.Bytes)
	case "PUBLIC KEY":
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		rsaKey, ok := key.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("PKIX key is not RSA")
		}
		return rsaKey, nil
	default:
		return nil, fmt.Errorf("unsupported PEM type: %s", block.Type)
	}
}

// ---------------------------------------------------------------------------
// OTel
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Env helpers
// ---------------------------------------------------------------------------

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required env variable %q is not set", key))
	}
	return v
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
