package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap/zapcore"

	"github.com/Reddetk/SayMoDev/identy-service/adapter/primary/http/middleware"
	"github.com/Reddetk/SayMoDev/identy-service/adapter/secondary"
	"github.com/Reddetk/SayMoDev/identy-service/adapter/telemetry"
)

// Config -- агрегированная конфигурация identity-service.
// Имена переменных согласованы с cmd/main.go (источник правды).
//
// Обязательные переменные:
//   POSTGRES_DSN                 postgres://user:pass@host:5432/db?sslmode=disable
//   REDIS_URL                    redis://:pass@host:6379/0
//   REDIS_BLACKLIST_ADDR         host:6380
//   REDIS_BLACKLIST_PASSWORD     ...
//   JWT_PRIVATE_KEY_PATH         /run/secrets/private.pem
//   JWT_PUBLIC_KEY_PATH          /run/secrets/public.pem
//   JWT_KID                      key-v1
//   GOOGLE_CLIENT_ID             ...
//   GOOGLE_CLIENT_SECRET         ...
//   GOOGLE_REDIRECT_URI          https://...
//   POSTBOX_IAM_TOKEN            ...
//   POSTBOX_FROM_ADDRESS         noreply@saymo.ru
//
// Опциональные:
//   HTTP_ADDR                    :8080  (default)
//   JWT_PREV_PUBLIC_KEY_PATH     /run/secrets/prev_public.pem
//   JWT_PREV_KID                 key-v0
//   OTEL_EXPORTER_OTLP_ENDPOINT  localhost:4317  (default)
//   SERVICE_NAME                 identity-service  (default)
//   SERVICE_VERSION              dev  (default)
//   ENVIRONMENT                  development  (default)
//   LOG_LEVEL                    info  (default)
//   LOG_DEVELOPMENT              false  (default)
//   CORS_ALLOWED_ORIGINS         http://localhost:3000,...
type Config struct {
	HTTPAddr    string
	PostgresDSN string
	RedisURL    string

	RedisBlacklistAddr     string
	RedisBlacklistPassword string

	JWTPrivateKeyPath string
	JWTPublicKeyPath  string
	JWTKid            string
	JWTPrevPublicKey  string // optional: путь к предыдущему публичному ключу (rotation overlap)
	JWTPrevKid        string // optional

	OTelEndpoint string

	GoogleOAuth secondary.GoogleOAuthConfig
	Postbox     secondary.PostboxConfig
	CORS        middleware.CORSConfig
	Logger      telemetry.LoggerConfig
	Trace       telemetry.TracerConfig
}

// Load читает переменные окружения и возвращает заполненный Config.
// Отсутствие любого обязательного поля -- ошибка.
func Load() (*Config, error) {
	cfg := &Config{}
	var missing []string

	require := func(key string, dst *string) {
		if v := os.Getenv(key); v != "" {
			*dst = v
		} else {
			missing = append(missing, key)
		}
	}

	// --- infrastructure ------------------------------------------------------
	cfg.HTTPAddr = envOr("HTTP_ADDR", ":8080")
	cfg.OTelEndpoint = envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")

	require("POSTGRES_DSN", &cfg.PostgresDSN)
	require("REDIS_URL", &cfg.RedisURL)
	require("REDIS_BLACKLIST_ADDR", &cfg.RedisBlacklistAddr)
	require("REDIS_BLACKLIST_PASSWORD", &cfg.RedisBlacklistPassword)

	// --- JWT -----------------------------------------------------------------
	require("JWT_PRIVATE_KEY_PATH", &cfg.JWTPrivateKeyPath)
	require("JWT_PUBLIC_KEY_PATH", &cfg.JWTPublicKeyPath)
	require("JWT_KID", &cfg.JWTKid)

	// optional: rotation overlap window (>= 7 days per spec)
	cfg.JWTPrevPublicKey = os.Getenv("JWT_PREV_PUBLIC_KEY_PATH")
	cfg.JWTPrevKid = os.Getenv("JWT_PREV_KID")

	// --- telemetry -----------------------------------------------------------
	serviceName := envOr("SERVICE_NAME", "identity-service")
	serviceVersion := envOr("SERVICE_VERSION", "dev")
	environment := envOr("ENVIRONMENT", "development")

	cfg.Logger = telemetry.LoggerConfig{
		ServiceName: serviceName,
		BC:          "bc1",
		Level:       parseLogLevel(os.Getenv("LOG_LEVEL")),
		Development: parseBool(os.Getenv("LOG_DEVELOPMENT"), false),
	}

	cfg.Trace = telemetry.TracerConfig{
		ServiceName:    serviceName,
		ServiceVersion: serviceVersion,
		Environment:    environment,
		JaegerEndpoint: cfg.OTelEndpoint,
	}

	// --- google oauth --------------------------------------------------------
	require("GOOGLE_CLIENT_ID", &cfg.GoogleOAuth.ClientID)
	require("GOOGLE_CLIENT_SECRET", &cfg.GoogleOAuth.ClientSecret)
	require("GOOGLE_REDIRECT_URI", &cfg.GoogleOAuth.RedirectURI)

	// --- postbox -------------------------------------------------------------
	require("POSTBOX_IAM_TOKEN", &cfg.Postbox.IAMToken)
	require("POSTBOX_FROM_ADDRESS", &cfg.Postbox.FromAddress)

	// --- cors ----------------------------------------------------------------
	cfg.CORS = middleware.CORSConfig{
		AllowedOrigins:   splitCSV(os.Getenv("CORS_ALLOWED_ORIGINS")),
		AllowedMethods:   splitCSVOr("CORS_ALLOWED_METHODS", "GET,POST,PUT,DELETE,OPTIONS"),
		AllowedHeaders:   splitCSVOr("CORS_ALLOWED_HEADERS", "Content-Type,Authorization"),
		ExposedHeaders:   splitCSV(os.Getenv("CORS_EXPOSED_HEADERS")),
		AllowCredentials: parseBool(os.Getenv("CORS_ALLOW_CREDENTIALS"), true),
		MaxAge:           parseInt(os.Getenv("CORS_MAX_AGE"), 600),
	}

	// --- validate ------------------------------------------------------------
	if len(missing) > 0 {
		return nil, fmt.Errorf("config: missing required env vars: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}

// OTelConnectTimeout возвращает OTEL_CONNECT_TIMEOUT_SECONDS или 0 (дефолт 5s в tracer).
func (c *Config) OTelConnectTimeout() time.Duration {
	v := parseInt(os.Getenv("OTEL_CONNECT_TIMEOUT_SECONDS"), 0)
	if v <= 0 {
		return 0
	}
	return time.Duration(v) * time.Second
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

func splitCSVOr(key, fallback string) []string {
	if v := os.Getenv(key); v != "" {
		return splitCSV(v)
	}
	return splitCSV(fallback)
}

func parseBool(s string, def bool) bool {
	if s == "" {
		return def
	}
	v, err := strconv.ParseBool(s)
	if err != nil {
		return def
	}
	return v
}

func parseInt(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

func parseLogLevel(s string) zapcore.Level {
	switch strings.ToLower(s) {
	case "debug":
		return zapcore.DebugLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}
