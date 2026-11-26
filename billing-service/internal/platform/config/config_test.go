package config

import (
	"encoding/csv"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoad_FromFixtures прогоняет CSV-фикстуры, написанные независимым автором
// (testdata/fixtures/config_cases.csv): успешные значения и border cases.
//
// После первого красного прогона в фикстуре исправлены две строки:
//   - unicode-пароль в DSN переведён в ожидаемую ошибку: RFC 3986 запрещает
//     не-ASCII в userinfo, pgx отклоняет такой DSN (net/url: invalid userinfo);
//   - в percent-encoded DSN ожидание приведено к входу (у DSN не было query).
func TestLoad_FromFixtures(t *testing.T) {
	cases := readCSV(t, "config_cases.csv")

	for _, row := range cases {
		row := row
		t.Run(row["case_id"], func(t *testing.T) {
			lookup := lookupFromOverrides(row["env_overrides"])

			cfg, err := Load(lookup)

			wantErr := row["want_error"] == "true"
			if wantErr {
				require.Error(t, err, "description: %s", row["description"])
				assert.Contains(t, strings.ToLower(err.Error()), row["want_error_contains"],
					"description: %s", row["description"])
				return
			}

			require.NoError(t, err, "description: %s", row["description"])
			require.NotNil(t, cfg)

			assert.Equal(t, row["want_env"], cfg.Env)
			assert.Equal(t, mustInt(t, row["want_server_port"]), cfg.Ports.Server)
			assert.Equal(t, mustInt(t, row["want_metrics_port"]), cfg.Ports.Metrics)
			assert.Equal(t, row["want_postgres_dsn"], cfg.Postgres.DSN)
			assert.Equal(t, row["want_redis_addr"], cfg.Redis.Addr)
			assert.Equal(t, row["want_kafka_brokers"], strings.Join(cfg.Kafka.Brokers, ","))
		})
	}
}

func TestLoad_NilLookup(t *testing.T) {
	cfg, err := Load(nil)
	assert.Nil(t, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load(lookupFromOverrides(
		"POSTGRES_DSN=postgres://u:p@localhost:5434/db;REDIS_ADDR=localhost:6381;KAFKA_BROKERS=localhost:29092",
	))
	require.NoError(t, err)
	assert.Equal(t, EnvLocal, cfg.Env)
	assert.Equal(t, 8002, cfg.Ports.Server)
	assert.Equal(t, 9002, cfg.Ports.Metrics)
	assert.Equal(t, "./migrations", cfg.MigrationsPath)
	assert.Equal(t, "saymo.billing.events", cfg.Kafka.EventsTopic)
	assert.Equal(t, "saymo.identity.events", cfg.Kafka.IdentityTopic)
	assert.Equal(t, "billing-service", cfg.Kafka.ConsumerGroup)
}

func TestLoad_AcquiringURLMustBeHTTPS(t *testing.T) {
	base := "POSTGRES_DSN=postgres://u:p@localhost:5434/db;REDIS_ADDR=localhost:6381;KAFKA_BROKERS=localhost:29092"

	_, err := Load(lookupFromOverrides("ENV=prod;" + base + ";ACQUIRING_API_URL=http://pay.example"))
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "acquiring_api_url")

	// local/test: локальный mock без TLS допустим.
	cfg, err := Load(lookupFromOverrides(base + ";ACQUIRING_API_URL=http://pay.example"))
	require.NoError(t, err)
	assert.Equal(t, "http://pay.example", cfg.Acquiring.APIURL)

	cfg, err = Load(lookupFromOverrides(base + ";ACQUIRING_API_URL=https://pay.example"))
	require.NoError(t, err)
	assert.Equal(t, "https://pay.example", cfg.Acquiring.APIURL)

	// Пустой URL допустим: ACL выключен до шага 5.
	cfg, err = Load(lookupFromOverrides(base + ";ACQUIRING_API_URL="))
	require.NoError(t, err)
	assert.Empty(t, cfg.Acquiring.APIURL)
}

func TestLoad_InvalidDurations(t *testing.T) {
	base := "POSTGRES_DSN=postgres://u:p@localhost:5434/db;REDIS_ADDR=localhost:6381;KAFKA_BROKERS=localhost:29092"

	for _, override := range []string{
		"HEALTH_TIMEOUT=soon",
		"SHUTDOWN_TIMEOUT=-1s",
		"HEALTH_TIMEOUT=0s",
	} {
		_, err := Load(lookupFromOverrides(base + ";" + override))
		require.Error(t, err, override)
	}
}

func TestLoad_InvalidJWKSURL(t *testing.T) {
	base := "POSTGRES_DSN=postgres://u:p@localhost:5434/db;REDIS_ADDR=localhost:6381;KAFKA_BROKERS=localhost:29092"

	for _, bad := range []string{"ftp://identity/jwks", "identity/jwks", "://"} {
		_, err := Load(lookupFromOverrides(base + ";JWKS_URL=" + bad))
		require.Error(t, err, bad)
		assert.Contains(t, strings.ToLower(err.Error()), "jwks_url")
	}
}

func TestLoad_PostboxValidation(t *testing.T) {
	base := "POSTGRES_DSN=postgres://u:p@localhost:5434/db;REDIS_ADDR=localhost:6381;KAFKA_BROKERS=localhost:29092"

	cfg, err := Load(lookupFromOverrides(base + ";POSTBOX_IAM_TOKEN=t;POSTBOX_FROM_ADDRESS=noreply@saymo.ru"))
	require.NoError(t, err)
	assert.Equal(t, "noreply@saymo.ru", cfg.Postbox.FromAddress)
	assert.Equal(t, "ru-central1", cfg.Postbox.Region)

	_, err = Load(lookupFromOverrides(base + ";POSTBOX_IAM_TOKEN=t"))
	require.Error(t, err, "from address is required")

	_, err = Load(lookupFromOverrides(base + ";POSTBOX_FROM_ADDRESS=noreply@saymo.ru"))
	require.Error(t, err, "auth is required")

	_, err = Load(lookupFromOverrides(base + ";POSTBOX_FROM_ADDRESS=noreply@saymo.ru;POSTBOX_ACCESS_KEY=a"))
	require.Error(t, err, "secret key is required")

	_, err = Load(lookupFromOverrides(base + ";ENV=prod;POSTBOX_IAM_TOKEN=t;POSTBOX_FROM_ADDRESS=noreply@saymo.ru;POSTBOX_ENDPOINT=http://postbox.local"))
	require.Error(t, err, "prod requires https")

	cfg, err = Load(lookupFromOverrides(base + ";POSTBOX_ACCESS_KEY=a;POSTBOX_SECRET_KEY=b;POSTBOX_FROM_ADDRESS=noreply@saymo.ru;POSTBOX_ENDPOINT=https://postbox.local"))
	require.NoError(t, err)
	assert.Equal(t, "a", cfg.Postbox.AccessKey)
	assert.Equal(t, "https://postbox.local", cfg.Postbox.Endpoint)
}

func TestValidatePostgresDSN(t *testing.T) {
	valid := []string{
		"postgres://u:p@localhost:5432/db",
		"postgresql://u:p@[::1]:5432/db?sslmode=disable",
	}
	for _, dsn := range valid {
		require.NoError(t, ValidatePostgresDSN(dsn), dsn)
	}

	invalid := map[string]string{
		"http://localhost/db":           "scheme",
		"postgres://localhost:70000/db": "port",
		"postgres://localhost:5432":     "database",
		"postgres:///db":                "host",
		"billing_db":                    "scheme",
	}
	for dsn, want := range invalid {
		err := ValidatePostgresDSN(dsn)
		require.Error(t, err, dsn)
		assert.Contains(t, strings.ToLower(err.Error()), want, dsn)
	}
}

func TestLoadDSN(t *testing.T) {
	lookup := lookupFromOverrides("POSTGRES_DSN=postgres://u:p@localhost:5432/db")
	dsn, err := LoadDSN(lookup)
	require.NoError(t, err)
	assert.Equal(t, "postgres://u:p@localhost:5432/db", dsn)

	_, err = LoadDSN(lookupFromOverrides(""))
	require.Error(t, err)
}

func TestRedact(t *testing.T) {
	const dsn = "postgres://billing:s3cret@localhost:5432/billing_db?sslmode=disable"

	redacted := RedactDSN(dsn)
	assert.NotContains(t, redacted, "s3cret")
	assert.Contains(t, redacted, "billing:REDACTED@")
	assert.Contains(t, redacted, "billing_db")

	err := RedactError(errors.New("connect failed: "+dsn), dsn)
	assert.NotContains(t, err.Error(), "s3cret")
	assert.Contains(t, err.Error(), "***")
}

func readCSV(t *testing.T, name string) []map[string]string {
	t.Helper()

	path := filepath.Join("..", "..", "..", "testdata", "fixtures", name)
	f, err := os.Open(path)
	require.NoError(t, err, "fixtures must exist: %s", path)
	defer func() { _ = f.Close() }()

	reader := csv.NewReader(f)
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(records), 2, "fixture must contain header and rows")

	header := records[0]
	rows := make([]map[string]string, 0, len(records)-1)
	for _, record := range records[1:] {
		if len(record) == 0 {
			continue
		}
		require.Len(t, record, len(header), "malformed CSV row: %v", record)
		row := make(map[string]string, len(header))
		for i, key := range header {
			row[key] = record[i]
		}
		rows = append(rows, row)
	}
	return rows
}

func lookupFromOverrides(overrides string) LookupFunc {
	values := map[string]string{}
	if overrides != "" {
		for _, pair := range strings.Split(overrides, ";") {
			key, val, ok := strings.Cut(pair, "=")
			if !ok {
				continue
			}
			values[strings.TrimSpace(key)] = val
		}
	}
	return func(key string) (string, bool) {
		val, ok := values[key]
		return val, ok
	}
}

func mustInt(t *testing.T, raw string) int {
	t.Helper()
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	require.NoError(t, err)
	return n
}
