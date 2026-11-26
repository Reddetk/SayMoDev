// Package config загружает и валидирует конфигурацию сервиса из переменных окружения.
// goca:manual — генератор goca v1.27.0 не покрывает строгую env-валидацию BC#3.
// Секретов в коде нет: значения по умолчанию существуют только для local/test.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment — режим запуска.
type Environment string

const (
	EnvLocal Environment = "local"
	EnvTest  Environment = "test"
	EnvProd  Environment = "prod"
)

// Config — полная конфигурация процесса.
type Config struct {
	Env         Environment
	LogLevel    string
	HTTPAddr    string
	MetricsAddr string

	PostgresDSN string

	RedisAddr          string
	RedisPassword      string
	RedisBlacklistAddr string
	RedisBlacklistPass string

	KafkaBrokers []string

	S3Endpoint  string
	S3Region    string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string

	JWKSURL            string
	BC4URL             string
	BC4ServiceToken    string
	JaegerOTLPEndpoint string

	CORSAllowedOrigins []string

	ShutdownTimeout time.Duration
	ProbeTimeout    time.Duration
}

// ValidationError — ошибка конфигурации с указанием поля.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("config: %s: %s", e.Field, e.Message)
}

// Load читает конфигурацию из окружения процесса.
func Load() (*Config, error) {
	return LoadFrom(os.LookupEnv)
}

// LoadFrom читает конфигурацию через переданный lookup (нужен для тестов).
func LoadFrom(lookup func(string) (string, bool)) (*Config, error) {
	getRaw := func(key string) (string, bool) {
		return lookup(key)
	}
	get := func(key string) string {
		v, _ := lookup(key)
		return strings.TrimSpace(v)
	}

	envRaw, envPresent := getRaw("ENV")
	if !envPresent || strings.TrimSpace(envRaw) == "" {
		return nil, &ValidationError{"ENV", "is required (local|test|prod)"}
	}
	env := Environment(strings.ToLower(strings.TrimSpace(envRaw)))
	switch env {
	case EnvLocal, EnvTest, EnvProd:
	default:
		return nil, &ValidationError{"ENV", fmt.Sprintf("must be one of local|test|prod, got %q", env)}
	}

	isProd := env == EnvProd

	httpAddr, err := resolveAddr("HTTP_ADDR", "HTTP_PORT", getRaw, "8003")
	if err != nil {
		return nil, err
	}
	metricsAddr, err := resolveAddr("METRICS_ADDR", "METRICS_PORT", getRaw, "9003")
	if err != nil {
		return nil, err
	}

	bucket, bucketPresent := getRaw("S3_BUCKET")

	cfg := &Config{
		Env:         env,
		LogLevel:    strings.ToLower(defaultString(get("LOG_LEVEL"), "info")),
		HTTPAddr:    httpAddr,
		MetricsAddr: metricsAddr,

		PostgresDSN: get("POSTGRES_DSN"),

		RedisAddr:          get("REDIS_ADDR"),
		RedisPassword:      get("REDIS_PASSWORD"),
		RedisBlacklistAddr: get("REDIS_BLACKLIST_ADDR"),
		RedisBlacklistPass: get("REDIS_BLACKLIST_PASSWORD"),

		KafkaBrokers: splitList(get("KAFKA_BROKERS")),

		S3Endpoint:  get("S3_ENDPOINT"),
		S3Region:    defaultString(get("S3_REGION"), "ru-central1"),
		S3Bucket:    strings.TrimSpace(bucket),
		S3AccessKey: get("S3_ACCESS_KEY"),
		S3SecretKey: get("S3_SECRET_KEY"),

		JWKSURL:            get("JWKS_URL"),
		BC4URL:             get("BC4_URL"),
		BC4ServiceToken:    get("BC4_SERVICE_TOKEN"),
		JaegerOTLPEndpoint: get("JAEGER_OTLP_ENDPOINT"),

		CORSAllowedOrigins: splitList(defaultString(get("CORS_ALLOWED_ORIGINS"), "http://localhost:3000,http://localhost:5173")),

		ShutdownTimeout: 15 * time.Second,
		ProbeTimeout:    2 * time.Second,
	}

	if !isProd {
		if cfg.PostgresDSN == "" {
			cfg.PostgresDSN = "postgres://content:content_local_pw@localhost:5435/content_db?sslmode=disable"
		}
		if cfg.RedisAddr == "" {
			cfg.RedisAddr = "localhost:6382"
		}
		if cfg.RedisPassword == "" {
			cfg.RedisPassword = "redis_local_pw"
		}
		if len(cfg.KafkaBrokers) == 0 {
			cfg.KafkaBrokers = []string{"localhost:9092"}
		}
		if cfg.S3Endpoint == "" {
			cfg.S3Endpoint = "http://localhost:9005"
		}
		if cfg.S3Bucket == "" && !bucketPresent {
			cfg.S3Bucket = "saymo-programs"
		}
		if cfg.S3AccessKey == "" {
			cfg.S3AccessKey = "minioadmin"
		}
		if cfg.S3SecretKey == "" {
			cfg.S3SecretKey = "minioadmin"
		}
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return &ValidationError{"LOG_LEVEL", fmt.Sprintf("must be debug|info|warn|error, got %q", c.LogLevel)}
	}

	if err := validateHostPort("HTTP_ADDR", c.HTTPAddr); err != nil {
		return err
	}
	if err := validateHostPort("METRICS_ADDR", c.MetricsAddr); err != nil {
		return err
	}
	if c.HTTPAddr == c.MetricsAddr {
		return &ValidationError{"METRICS_ADDR", "must differ from HTTP_ADDR"}
	}

	if c.PostgresDSN == "" {
		return &ValidationError{"POSTGRES_DSN", "is required"}
	}
	u, err := url.Parse(c.PostgresDSN)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" || strings.Trim(u.Path, "/") == "" {
		return &ValidationError{"POSTGRES_DSN", "must be a postgres:// URL with host and database"}
	}

	if err := validateHostPort("REDIS_ADDR", c.RedisAddr); err != nil {
		return err
	}
	if c.RedisBlacklistAddr != "" {
		if err := validateHostPort("REDIS_BLACKLIST_ADDR", c.RedisBlacklistAddr); err != nil {
			return err
		}
	}

	if len(c.KafkaBrokers) == 0 {
		return &ValidationError{"KAFKA_BROKERS", "at least one broker is required"}
	}
	for _, broker := range c.KafkaBrokers {
		if err := validateHostPort("KAFKA_BROKERS", broker); err != nil {
			return err
		}
	}

	ep, err := url.Parse(c.S3Endpoint)
	if err != nil || (ep.Scheme != "http" && ep.Scheme != "https") || ep.Host == "" {
		return &ValidationError{"S3_ENDPOINT", "must be an http(s) URL with host"}
	}
	if c.Env == EnvProd && ep.Scheme != "https" {
		return &ValidationError{"S3_ENDPOINT", "must use https in prod (presigned PDF URLs)"}
	}
	if c.S3Bucket == "" {
		return &ValidationError{"S3_BUCKET", "is required"}
	}
	if c.S3AccessKey == "" {
		return &ValidationError{"S3_ACCESS_KEY", "is required"}
	}
	if c.S3SecretKey == "" {
		return &ValidationError{"S3_SECRET_KEY", "is required"}
	}

	for _, origin := range c.CORSAllowedOrigins {
		if origin == "*" {
			continue
		}
		u, err := url.Parse(origin)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return &ValidationError{"CORS_ALLOWED_ORIGINS", fmt.Sprintf("must be * or a list of absolute origins, got %q", origin)}
		}
	}

	if c.Env == EnvProd {
		for field, value := range map[string]string{
			"POSTGRES_DSN":   c.PostgresDSN,
			"REDIS_PASSWORD": c.RedisPassword,
			"S3_ACCESS_KEY":  c.S3AccessKey,
			"S3_SECRET_KEY":  c.S3SecretKey,
		} {
			if isPlaceholder(value) {
				return &ValidationError{field, "prod requires a real value, placeholder detected"}
			}
		}

		if c.JWKSURL == "" {
			return &ValidationError{"JWKS_URL", "is required in prod"}
		}
		jwks, err := url.Parse(c.JWKSURL)
		if err != nil || jwks.Scheme != "https" || jwks.Host == "" {
			return &ValidationError{"JWKS_URL", "must be an https URL in prod"}
		}
		if c.RedisBlacklistAddr == "" {
			return &ValidationError{"REDIS_BLACKLIST_ADDR", "is required in prod"}
		}
		if isPlaceholder(c.RedisBlacklistPass) {
			return &ValidationError{"REDIS_BLACKLIST_PASSWORD", "prod requires a real value, placeholder detected"}
		}
	}

	return nil
}

func validateHostPort(field, value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return &ValidationError{field, fmt.Sprintf("must be host:port, got %q", value)}
	}
	if host == "" && !strings.HasPrefix(value, ":") {
		return &ValidationError{field, fmt.Sprintf("empty host in %q", value)}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return &ValidationError{field, fmt.Sprintf("port must be 1..65535, got %q", port)}
	}
	return nil
}

// resolveAddr принимает либо полный ADDR (host:port), либо отдельный PORT.
// Presence важнее значения: явно пустая переменная — ошибка, а не повод взять дефолт.
func resolveAddr(addrKey, portKey string, lookup func(string) (string, bool), defaultPort string) (string, error) {
	if raw, ok := lookup(addrKey); ok {
		value := strings.TrimSpace(raw)
		if value == "" {
			return "", &ValidationError{addrKey, "must not be empty"}
		}
		if err := validateHostPort(addrKey, value); err != nil {
			return "", err
		}
		return value, nil
	}

	if raw, ok := lookup(portKey); ok {
		n, err := parsePortStrict(portKey, raw)
		if err != nil {
			return "", err
		}
		return ":" + strconv.Itoa(n), nil
	}

	return ":" + defaultPort, nil
}

func parsePortStrict(field, raw string) (int, error) {
	if raw == "" {
		return 0, &ValidationError{field, "must not be empty"}
	}
	if raw != strings.TrimSpace(raw) {
		return 0, &ValidationError{field, fmt.Sprintf("must not contain spaces, got %q", raw)}
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, &ValidationError{field, fmt.Sprintf("must be a number, got %q", raw)}
		}
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 65535 {
		return 0, &ValidationError{field, fmt.Sprintf("port must be 1..65535, got %q", raw)}
	}
	return n, nil
}

func isPlaceholder(v string) bool {
	if v == "" {
		return true
	}
	l := strings.ToLower(v)
	for _, marker := range []string{"change_me", "change-me", "changeme", "your-", "replace-me", "minioadmin"} {
		if strings.Contains(l, marker) {
			return true
		}
	}
	return false
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func splitList(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
