// Package config загружает и валидирует конфигурацию billing-service (BC#2).
//
// Все параметры приходят из переменных окружения (deployment/README.md):
// никаких хардкодных значений, секреты — только через env-файлы вне репозитория.
package config

// goca:manual — конфигурация BC#2 (env-контракт) не генерируется goca init.

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Environment — допустимые среды развёртывания.
const (
	EnvLocal = "local"
	EnvTest  = "test"
	EnvProd  = "prod"
)

// Config — полная конфигурация сервиса.
type Config struct {
	Env   string
	Ports Ports

	Postgres Postgres
	Redis    Redis
	Kafka    Kafka

	JWT       JWT
	Acquiring Acquiring
	Postbox   Postbox

	MigrationsPath    string
	HealthTimeout     time.Duration
	ShutdownTimeout   time.Duration
	LifecycleInterval time.Duration
	LogLevel          string
	LogFormat         string
	CORSOrigins       []string
	OTELEndpoint      string
}

// Ports — HTTP-порты сервиса. API и metrics разделены (Observability.md, Service Ports).
type Ports struct {
	Server  int
	Metrics int
}

// Postgres — параметры подключения к billing_db.
type Postgres struct {
	DSN string
}

// Redis — собственный L2-инстанс bc2 (idempotency, tariff cache, rate limit).
type Redis struct {
	Addr          string
	Password      string
	BlacklistAddr string
	BlacklistPass string
}

// Kafka — брокеры и имена топиков BC#2.
type Kafka struct {
	Brokers       []string
	EventsTopic   string
	IdentityTopic string
	ConsumerGroup string
}

// JWT — параметры валидации токенов BC#1 (используются с шага 4).
type JWT struct {
	Issuer        string
	Audience      string
	JWKSURL       string
	PublicKeyPath string
}

// Acquiring — ACL-адаптер WebPay.
type Acquiring struct {
	APIURL    string
	SecretKey string
	StoreID   string
	ReturnURL string
	NotifyURL string
}

// Postbox - ACL-адаптер Yandex Cloud Postbox.
type Postbox struct {
	Endpoint         string
	Token            string
	FromAddress      string
	AccessKey        string
	SecretKey        string
	Region           string
	DefaultRecipient string
}

// LookupFunc — источник переменных окружения (os.LookupEnv в проде, map в тестах).
type LookupFunc func(key string) (string, bool)

// Load читает конфигурацию из окружения процесса.
func Load(lookup LookupFunc) (*Config, error) {
	if lookup == nil {
		return nil, fmt.Errorf("config: lookup function is nil")
	}

	env, err := parseEnv(lookup)
	if err != nil {
		return nil, err
	}

	serverPort, err := parsePort("SERVER_PORT", lookup, 8002)
	if err != nil {
		return nil, err
	}
	metricsPort, err := parsePort("METRICS_PORT", lookup, 9002)
	if err != nil {
		return nil, err
	}
	if metricsPort == serverPort {
		return nil, fmt.Errorf("config: metrics_port must differ from server_port (%d)", serverPort)
	}

	dsn, err := required("POSTGRES_DSN", lookup)
	if err != nil {
		return nil, err
	}
	if err := validatePostgresDSN(dsn); err != nil {
		return nil, fmt.Errorf("config: postgres_dsn: %w", err)
	}

	redisAddr, err := required("REDIS_ADDR", lookup)
	if err != nil {
		return nil, err
	}
	if err := validateHostPort(redisAddr); err != nil {
		return nil, fmt.Errorf("config: redis_addr: %w", err)
	}

	brokers, err := parseBrokers(lookup)
	if err != nil {
		return nil, err
	}

	jwksURL := value(lookup, "JWKS_URL", "http://localhost:8001/jwks.json")
	if jwksURL != "" {
		u, err := url.Parse(jwksURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("config: jwks_url: must be an absolute http(s) URL")
		}
	}

	acquiringURL := value(lookup, "ACQUIRING_API_URL", "")
	if acquiringURL != "" {
		u, err := url.Parse(acquiringURL)
		if err != nil || u.Host == "" || (u.Scheme != "https" && !(env != EnvProd && u.Scheme == "http")) {
			return nil, fmt.Errorf("config: acquiring_api_url: must be an absolute %s URL", map[bool]string{true: "https", false: "http(s) (local/test)"}[env == EnvProd])
		}
	}

	healthTimeout, err := parseDuration("HEALTH_TIMEOUT", lookup, 2*time.Second)
	if err != nil {
		return nil, err
	}
	shutdownTimeout, err := parseDuration("SHUTDOWN_TIMEOUT", lookup, 15*time.Second)
	if err != nil {
		return nil, err
	}
	lifecycleInterval, err := parseDuration("LIFECYCLE_INTERVAL", lookup, 15*time.Minute)
	if err != nil {
		return nil, err
	}
	if healthTimeout <= 0 {
		return nil, fmt.Errorf("config: health_timeout must be positive")
	}
	if shutdownTimeout <= 0 {
		return nil, fmt.Errorf("config: shutdown_timeout must be positive")
	}
	if lifecycleInterval <= 0 {
		return nil, fmt.Errorf("config: lifecycle_interval must be positive")
	}

	postbox := Postbox{
		Endpoint:         value(lookup, "POSTBOX_ENDPOINT", ""),
		Token:            value(lookup, "POSTBOX_IAM_TOKEN", ""),
		FromAddress:      value(lookup, "POSTBOX_FROM_ADDRESS", ""),
		AccessKey:        value(lookup, "POSTBOX_ACCESS_KEY", ""),
		SecretKey:        value(lookup, "POSTBOX_SECRET_KEY", ""),
		Region:           value(lookup, "POSTBOX_REGION", "ru-central1"),
		DefaultRecipient: value(lookup, "POSTBOX_DEFAULT_RECIPIENT", ""),
	}
	if postbox.Endpoint != "" {
		u, err := url.Parse(postbox.Endpoint)
		if err != nil || u.Host == "" || (u.Scheme != "https" && !(env != EnvProd && u.Scheme == "http")) {
			return nil, fmt.Errorf("config: postbox_endpoint: must be an absolute %s URL", map[bool]string{true: "https", false: "http(s) (local/test)"}[env == EnvProd])
		}
	}
	if postbox.Token != "" || postbox.AccessKey != "" || postbox.FromAddress != "" {
		if postbox.FromAddress == "" {
			return nil, fmt.Errorf("config: postbox_from_address is required when Postbox is configured")
		}
		if postbox.Token == "" && (postbox.AccessKey == "" || postbox.SecretKey == "") {
			return nil, fmt.Errorf("config: postbox requires POSTBOX_IAM_TOKEN or POSTBOX_ACCESS_KEY+POSTBOX_SECRET_KEY")
		}
	}

	return &Config{
		Env: env,
		Ports: Ports{
			Server:  serverPort,
			Metrics: metricsPort,
		},
		Postgres: Postgres{DSN: dsn},
		Redis: Redis{
			Addr:          redisAddr,
			Password:      value(lookup, "REDIS_PASSWORD", ""),
			BlacklistAddr: value(lookup, "REDIS_BLACKLIST_ADDR", "localhost:6380"),
			BlacklistPass: value(lookup, "REDIS_BLACKLIST_PASSWORD", ""),
		},
		Kafka: Kafka{
			Brokers:       brokers,
			EventsTopic:   value(lookup, "KAFKA_EVENTS_TOPIC", "saymo.billing.events"),
			IdentityTopic: value(lookup, "KAFKA_IDENTITY_TOPIC", "saymo.identity.events"),
			ConsumerGroup: value(lookup, "KAFKA_CONSUMER_GROUP", "billing-service"),
		},
		JWT: JWT{
			Issuer:        value(lookup, "JWT_ISSUER", "urn:saymo:identity-service"),
			Audience:      value(lookup, "JWT_AUDIENCE", "api.saymo"),
			JWKSURL:       jwksURL,
			PublicKeyPath: value(lookup, "JWT_PUBLIC_KEY_PATH", ""),
		},
		Acquiring: Acquiring{
			APIURL:    acquiringURL,
			SecretKey: value(lookup, "ACQUIRING_SECRET_KEY", ""),
			StoreID:   value(lookup, "ACQUIRING_STORE_ID", ""),
			ReturnURL: value(lookup, "ACQUIRING_RETURN_URL", ""),
			NotifyURL: value(lookup, "ACQUIRING_NOTIFY_URL", ""),
		},
		Postbox:           postbox,
		MigrationsPath:    value(lookup, "MIGRATIONS_PATH", "./migrations"),
		HealthTimeout:     healthTimeout,
		ShutdownTimeout:   shutdownTimeout,
		LifecycleInterval: lifecycleInterval,
		LogLevel:          value(lookup, "LOG_LEVEL", "info"),
		LogFormat:         value(lookup, "LOG_FORMAT", "json"),
		CORSOrigins:       splitList(value(lookup, "CORS_ALLOWED_ORIGINS", "http://localhost:3000,http://localhost:5173")),
		OTELEndpoint:      value(lookup, "OTEL_EXPORTER_OTLP_ENDPOINT", ""),
	}, nil
}

func parseEnv(lookup LookupFunc) (string, error) {
	raw := value(lookup, "ENV", EnvLocal)
	switch raw {
	case EnvLocal, EnvTest, EnvProd:
		return raw, nil
	default:
		return "", fmt.Errorf("config: env: unsupported value %q (want local|test|prod)", raw)
	}
}

func parsePort(name string, lookup LookupFunc, def int) (int, error) {
	raw := value(lookup, name, strconv.Itoa(def))
	port, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s: not an integer: %q", name, raw)
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("config: %s: port %d out of range 1..65535", name, port)
	}
	return port, nil
}

func parseDuration(name string, lookup LookupFunc, def time.Duration) (time.Duration, error) {
	raw := value(lookup, name, def.String())
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s: invalid duration %q", name, raw)
	}
	return d, nil
}

func parseBrokers(lookup LookupFunc) ([]string, error) {
	raw, err := required("KAFKA_BROKERS", lookup)
	if err != nil {
		return nil, err
	}

	parts := strings.Split(raw, ",")
	brokers := make([]string, 0, len(parts))
	for _, part := range parts {
		broker := strings.TrimSpace(part)
		if broker == "" {
			return nil, fmt.Errorf("config: kafka_brokers: empty entry in %q", raw)
		}
		if err := validateHostPort(broker); err != nil {
			return nil, fmt.Errorf("config: kafka_brokers: %q: %w", broker, err)
		}
		brokers = append(brokers, broker)
	}
	if len(brokers) == 0 {
		return nil, fmt.Errorf("config: kafka_brokers: no brokers configured")
	}
	return brokers, nil
}

func validatePostgresDSN(dsn string) error {
	u, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("malformed URL: %w", err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return fmt.Errorf("unsupported scheme %q (want postgres|postgresql)", u.Scheme)
	}
	if u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("missing host")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("port %q out of range 1..65535", port)
		}
	}
	if u.Path == "" || u.Path == "/" {
		return fmt.Errorf("missing database name")
	}
	return nil
}

// ValidatePostgresDSN — публичная валидация для cmd/migrate (единый контракт).
func ValidatePostgresDSN(dsn string) error { return validatePostgresDSN(dsn) }

// LoadDSN читает и валидирует POSTGRES_DSN без остальных зависимостей сервиса.
func LoadDSN(lookup LookupFunc) (string, error) {
	dsn, err := required("POSTGRES_DSN", lookup)
	if err != nil {
		return "", err
	}
	if err := validatePostgresDSN(dsn); err != nil {
		return "", fmt.Errorf("config: postgres_dsn: %w", err)
	}
	return dsn, nil
}

// RedactDSN скрывает пароль для логов и ошибок подключения.
func RedactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil {
		return "***"
	}
	user := u.User.Username()
	if _, hasPassword := u.User.Password(); hasPassword {
		u.User = url.UserPassword(user, "REDACTED")
	}
	return u.String()
}

// RedactError заменяет секреты в тексте ошибки (драйверы печатают DSN целиком).
func RedactError(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "***")
		}
	}
	return fmt.Errorf("%s", text)
}

func validateHostPort(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("want host:port: %w", err)
	}
	if host == "" {
		return fmt.Errorf("missing host")
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("port is not an integer: %q", port)
	}
	if n < 1 || n > 65535 {
		return fmt.Errorf("port %d out of range 1..65535", n)
	}
	return nil
}

func required(name string, lookup LookupFunc) (string, error) {
	raw, ok := lookup(name)
	if !ok || raw == "" {
		return "", fmt.Errorf("config: %s: required variable is not set", name)
	}
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("config: %s: empty value (whitespace only)", name)
	}
	return strings.TrimSpace(raw), nil
}

func value(lookup LookupFunc, name, def string) string {
	if raw, ok := lookup(name); ok {
		return raw
	}
	return def
}

func splitList(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
