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
// Заполняется через Load() из переменных окружения.
//
// Соглашение по env vars:
//   HTTP_ADDR            :8080
//   DATABASE_URL         postgres://user:pass@host:5432/db
//   REDIS_URL            redis://host:6379
//   RSA_KEY_PATH         /run/secrets/rsa_private.pem
//   JWKS_PATH            /run/secrets/jwks.json
//   OTEL_ENDPOINT        host:4317  (OTLP gRPC)
//   SERVICE_NAME         identity-service
//   SERVICE_VERSION      v1.0.0
//   ENVIRONMENT          production | staging | development
//   LOG_LEVEL            info | debug | warn | error  (default: info)
//   LOG_DEVELOPMENT      true | false                  (default: false)
//   GOOGLE_CLIENT_ID     ...
//   GOOGLE_CLIENT_SECRET ...
//   GOOGLE_REDIRECT_URI  https://...
//   POSTBOX_IAM_TOKEN    ...
//   POSTBOX_FROM_ADDRESS noreply@saymo.ru
//   CORS_ALLOWED_ORIGINS https://app.saymo.ru,https://staging.saymo.ru
//   CORS_ALLOWED_METHODS GET,POST,PUT,DELETE,OPTIONS
//   CORS_ALLOWED_HEADERS Content-Type,Authorization
//   CORS_EXPOSED_HEADERS X-Request-Id
//   CORS_ALLOW_CREDENTIALS true | false  (default: true)
//   CORS_MAX_AGE         600  (seconds, default: 600)
type Config struct {
	HTTPAddr     string // :8080
	DatabaseURL  string // postgres://...
	RedisURL     string // redis://...
	RSAKeyPath   string // путь к PEM приватного ключа
	JWKSPath     string // путь к JWKS JSON (публичные ключи)
	OTelEndpoint string // gRPC OTLP endpoint (Jaeger/Tempo)
	GoogleOAuth  secondary.GoogleOAuthConfig
	Postbox      secondary.PostboxConfig
	CORS         middleware.CORSConfig
	Logger       telemetry.LoggerConfig
	Trace        telemetry.TracerConfig
}

// Load читает переменные окружения и возвращает заполненный Config.
// Все обязательные поля проверяются; отсутствие любого -- ошибка.
func Load() (*Config, error) {
	cfg := &Config{}
	var missing []string

	// --- infrastructure -----------------------------------------------------
	cfg.HTTPAddr = envOr("HTTP_ADDR", ":8080")

	if v := os.Getenv("DATABASE_URL"); v != "" {
		cfg.DatabaseURL = v
	} else {
		missing = append(missing, "DATABASE_URL")
	}

	if v := os.Getenv("REDIS_URL"); v != "" {
		cfg.RedisURL = v
	} else {
		missing = append(missing, "REDIS_URL")
	}

	if v := os.Getenv("RSA_KEY_PATH"); v != "" {
		cfg.RSAKeyPath = v
	} else {
		missing = append(missing, "RSA_KEY_PATH")
	}

	if v := os.Getenv("JWKS_PATH"); v != "" {
		cfg.JWKSPath = v
	} else {
		missing = append(missing, "JWKS_PATH")
	}

	cfg.OTelEndpoint = envOr("OTEL_ENDPOINT", "localhost:4317")

	// --- telemetry ----------------------------------------------------------
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
		// ConnectTimeout: 0 -- telemetry.NewTracer использует дефолт 5s.
	}

	// --- google oauth -------------------------------------------------------
	if v := os.Getenv("GOOGLE_CLIENT_ID"); v != "" {
		cfg.GoogleOAuth.ClientID = v
	} else {
		missing = append(missing, "GOOGLE_CLIENT_ID")
	}

	if v := os.Getenv("GOOGLE_CLIENT_SECRET"); v != "" {
		cfg.GoogleOAuth.ClientSecret = v
	} else {
		missing = append(missing, "GOOGLE_CLIENT_SECRET")
	}

	if v := os.Getenv("GOOGLE_REDIRECT_URI"); v != "" {
		cfg.GoogleOAuth.RedirectURI = v
	} else {
		missing = append(missing, "GOOGLE_REDIRECT_URI")
	}

	// --- postbox ------------------------------------------------------------
	if v := os.Getenv("POSTBOX_IAM_TOKEN"); v != "" {
		cfg.Postbox.IAMToken = v
	} else {
		missing = append(missing, "POSTBOX_IAM_TOKEN")
	}

	if v := os.Getenv("POSTBOX_FROM_ADDRESS"); v != "" {
		cfg.Postbox.FromAddress = v
	} else {
		missing = append(missing, "POSTBOX_FROM_ADDRESS")
	}

	// --- cors ---------------------------------------------------------------
	cfg.CORS = middleware.CORSConfig{
		AllowedOrigins:   splitCSV(os.Getenv("CORS_ALLOWED_ORIGINS")),
		AllowedMethods:   splitCSVOr("CORS_ALLOWED_METHODS", "GET,POST,PUT,DELETE,OPTIONS"),
		AllowedHeaders:   splitCSVOr("CORS_ALLOWED_HEADERS", "Content-Type,Authorization"),
		ExposedHeaders:   splitCSV(os.Getenv("CORS_EXPOSED_HEADERS")),
		AllowCredentials: parseBool(os.Getenv("CORS_ALLOW_CREDENTIALS"), true),
		MaxAge:           parseInt(os.Getenv("CORS_MAX_AGE"), 600),
	}

	// --- validate -----------------------------------------------------------
	if len(missing) > 0 {
		return nil, fmt.Errorf("config: missing required env vars: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
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
	case "dpanic":
		return zapcore.DPanicLevel
	default:
		// "info" и всё неизвестное -> InfoLevel (prod-safe default)
		return zapcore.InfoLevel
	}
}

// OTelConnectTimeout возвращает TracerConfig.ConnectTimeout из переменной
// OTEL_CONNECT_TIMEOUT_SECONDS. Используется если стандартный дефолт 5s недостаточен.
func (c *Config) OTelConnectTimeout() time.Duration {
	v := parseInt(os.Getenv("OTEL_CONNECT_TIMEOUT_SECONDS"), 0)
	if v <= 0 {
		return 0 // telemetry.NewTracer применит дефолт 5s
	}
	return time.Duration(v) * time.Second
}
