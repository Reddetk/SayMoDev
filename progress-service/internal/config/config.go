// Package config loads runtime configuration from environment variables.
// No hardcoded literals: every tunable comes from env with a documented default.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the full runtime configuration of progress-service.
type Config struct {
	Environment string
	HTTPAddr    string
	LogLevel    string
	LogFormat   string

	PostgresDSN         string
	PostgresMaxConns    int
	PostgresMinConns    int
	PostgresMaxLifetime time.Duration

	RedisAddr      string
	RedisPassword  string
	RedisDB        int
	CacheTTL       time.Duration
	LocalCacheTTL  time.Duration
	IdempotencyTTL time.Duration

	BlacklistAddr     string
	BlacklistPassword string
	BlacklistDB       int

	JWKSURL        string
	JWKSRefreshTTL time.Duration
	JWTIssuer      string
	JWTAudience    string

	BC3BaseURL            string
	BC3Timeout            time.Duration
	BC3RetryDelay         time.Duration
	BC3CircuitFailures    int
	BC3CircuitOpen        time.Duration
	BC3CircuitHalfOpenMax int

	KafkaBrokers    []string
	KafkaGroupID    string
	ProgressTopic   string
	IdentityTopic   string
	BillingTopic    string
	ContentTopic    string
	DeadLetterTopic string
	OutboxPollEvery time.Duration
	OutboxBatchSize int

	S3Endpoint       string
	S3Region         string
	S3Bucket         string
	S3AccessKey      string
	S3SecretKey      string
	S3PresignPutTTL  time.Duration
	S3PresignGetTTL  time.Duration
	S3ForcePathStyle bool

	MaxBodyBytes int64

	InternalServiceToken string
	CORSAllowedOrigins   []string
	DefaultWeekBitmap    int

	OTELEnabled  bool
	OTELEndpoint string
	ServiceName  string

	ShutdownTimeout time.Duration
}

// Load reads configuration and fails fast when a required value is absent.
func Load() (*Config, error) {
	environment := getEnv("ENV", "local")
	cfg := &Config{
		Environment: environment,
		HTTPAddr:    getEnv("HTTP_ADDR", ":8082"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),
		LogFormat:   getEnv("LOG_FORMAT", "json"),

		PostgresDSN:         os.Getenv("POSTGRES_DSN"),
		PostgresMaxConns:    getEnvInt("POSTGRES_MAX_CONNS", 20),
		PostgresMinConns:    getEnvInt("POSTGRES_MIN_CONNS", 2),
		PostgresMaxLifetime: getEnvDuration("POSTGRES_MAX_LIFETIME", time.Hour),

		RedisAddr:      getEnv("REDIS_ADDR", "localhost:6381"),
		RedisPassword:  os.Getenv("REDIS_PASSWORD"),
		RedisDB:        getEnvInt("REDIS_DB", 0),
		CacheTTL:       getEnvDuration("CACHE_TTL", 5*time.Minute),
		LocalCacheTTL:  getEnvDuration("LOCAL_CACHE_TTL", 60*time.Second),
		IdempotencyTTL: getEnvDuration("IDEMPOTENCY_TTL", 24*time.Hour),

		BlacklistAddr:     getEnv("REDIS_BLACKLIST_ADDR", "localhost:6380"),
		BlacklistPassword: os.Getenv("REDIS_BLACKLIST_PASSWORD"),
		BlacklistDB:       getEnvInt("REDIS_BLACKLIST_DB", 0),

		JWKSURL:        getEnv("JWKS_URL", "http://localhost:8099/jwks.json"),
		JWKSRefreshTTL: getEnvDuration("JWKS_REFRESH_TTL", time.Hour),
		JWTIssuer:      getEnv("JWT_ISSUER", "saymo-iam"),
		JWTAudience:    getEnv("JWT_AUDIENCE", "saymo"),

		BC3BaseURL:            getEnv("BC3_BASE_URL", "http://localhost:8081"),
		BC3Timeout:            getEnvDuration("BC3_TIMEOUT", 2*time.Second),
		BC3RetryDelay:         getEnvDuration("BC3_RETRY_DELAY", 500*time.Millisecond),
		BC3CircuitFailures:    getEnvInt("BC3_CIRCUIT_FAILURES", 5),
		BC3CircuitOpen:        getEnvDuration("BC3_CIRCUIT_OPEN", 30*time.Second),
		BC3CircuitHalfOpenMax: getEnvInt("BC3_CIRCUIT_HALF_OPEN_MAX", 2),

		KafkaBrokers:    splitCSV(os.Getenv("KAFKA_BROKERS")),
		KafkaGroupID:    getEnv("KAFKA_GROUP_ID", "progress-service"),
		ProgressTopic:   getEnv("KAFKA_PROGRESS_TOPIC", "saymo.progress.events"),
		IdentityTopic:   getEnv("KAFKA_IDENTITY_TOPIC", "saymo.identity.events"),
		BillingTopic:    getEnv("KAFKA_BILLING_TOPIC", "saymo.billing.events"),
		ContentTopic:    getEnv("KAFKA_CONTENT_TOPIC", "saymo.content.events"),
		DeadLetterTopic: getEnv("KAFKA_DLQ_TOPIC", "saymo.deadletter"),
		OutboxPollEvery: getEnvDuration("OUTBOX_POLL_INTERVAL", 2*time.Second),
		OutboxBatchSize: getEnvInt("OUTBOX_BATCH_SIZE", 50),

		S3Endpoint:       getEnv("S3_ENDPOINT", "https://storage.yandexcloud.net"),
		S3Region:         getEnv("S3_REGION", "ru-central1"),
		S3Bucket:         getEnv("S3_BUCKET", "saymo-answers"),
		S3AccessKey:      os.Getenv("S3_ACCESS_KEY"),
		S3SecretKey:      os.Getenv("S3_SECRET_KEY"),
		S3PresignPutTTL:  getEnvDuration("S3_PRESIGN_PUT_TTL", 15*time.Minute),
		S3PresignGetTTL:  getEnvDuration("S3_PRESIGN_GET_TTL", time.Hour),
		S3ForcePathStyle: getEnvBool("S3_FORCE_PATH_STYLE", false),

		MaxBodyBytes: int64(getEnvInt("HTTP_MAX_BODY_BYTES", 1<<20)),

		InternalServiceToken: os.Getenv("INTERNAL_SERVICE_TOKEN"),
		CORSAllowedOrigins:   splitCSV(os.Getenv("CORS_ALLOWED_ORIGINS")),
		DefaultWeekBitmap:    getEnvInt("TARIFF_DEFAULT_WEEK_BITMAP", defaultWeekBitmap(environment)),

		OTELEnabled:  getEnvBool("OTEL_ENABLED", false),
		OTELEndpoint: getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317"),
		ServiceName:  getEnv("OTEL_SERVICE_NAME", "progress-service"),

		ShutdownTimeout: getEnvDuration("SHUTDOWN_TIMEOUT", 15*time.Second),
	}

	if cfg.PostgresDSN == "" {
		return nil, fmt.Errorf("config: POSTGRES_DSN is required")
	}
	if cfg.JWKSURL == "" {
		return nil, fmt.Errorf("config: JWKS_URL is required")
	}
	if cfg.InternalServiceToken == "" {
		return nil, fmt.Errorf("config: INTERNAL_SERVICE_TOKEN is required (service-to-service deletion guard)")
	}
	return cfg, nil
}

// KafkaEnabled reports whether event consumption/production is configured.
func (c *Config) KafkaEnabled() bool { return len(c.KafkaBrokers) > 0 }

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
	}
	return def
}

func getEnvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return def
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
	}
	return def
}

func splitCSV(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// defaultWeekBitmap: local/test use the documented billing-stub default (127).
// Production grants nothing until a real tariff event arrives (invariant Rule 3).
func defaultWeekBitmap(env string) int {
	if env == "prod" {
		return 0
	}
	return 127
}
