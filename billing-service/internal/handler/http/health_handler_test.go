package httpapi

import (
	"context"
	"encoding/csv"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Reddetk/SayMoDev/billing-service/internal/platform/health"
	"github.com/Reddetk/SayMoDev/billing-service/internal/platform/metrics"
)

// TestHealthEndpoints_FromFixtures прогоняет CSV-фикстуры независимого автора:
// состояния зависимостей, методы, пути и ожидаемые коды.
func TestHealthEndpoints_FromFixtures(t *testing.T) {
	for _, row := range readFixture(t, "health_http_cases.csv") {
		row := row
		t.Run(row["case_id"], func(t *testing.T) {
			apiRouter, adminMux := newTestServers(t,
				row["postgres_ok"] == "true",
				row["redis_ok"] == "true",
				row["kafka_ok"] == "true",
			)

			var handler http.Handler = apiRouter
			if strings.HasPrefix(row["request_path"], "/metrics") {
				handler = adminMux
			}

			req := httptest.NewRequest(row["http_method"], row["request_path"], nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.Equal(t, mustAtoi(t, row["want_status"]), rec.Code,
				"description: %s; body: %s", row["description"], rec.Body.String())

			if row["want_json_status"] != "" {
				assert.Contains(t, rec.Body.String(), `"status":"`+row["want_json_status"]+`"`,
					"body: %s", rec.Body.String())
			}
			if row["want_services_contains"] != "" {
				assert.Contains(t, rec.Body.String(), row["want_services_contains"],
					"body: %s", rec.Body.String())
			}
			if rec.Code == http.StatusOK && row["request_path"] == "/metrics" {
				assert.Contains(t, rec.Header().Get("Content-Type"), "text/plain")
			}
		})
	}
}

func TestLive_ContentTypeIsPlainText(t *testing.T) {
	apiRouter, _ := newTestServers(t, true, true, true)

	rec := httptest.NewRecorder()
	apiRouter.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/live", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/plain")
	assert.Equal(t, "Alive", rec.Body.String())
}

func TestAdminLiveness(t *testing.T) {
	_, adminMux := newTestServers(t, false, false, false)

	rec := httptest.NewRecorder()
	adminMux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/live", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "Alive", rec.Body.String())
}

func newTestServers(t *testing.T, postgresOK, redisOK, kafkaOK bool) (http.Handler, http.Handler) {
	t.Helper()

	checker := func(ok bool) health.Checker {
		return health.CheckerFunc(func(context.Context) error {
			if ok {
				return nil
			}
			return errors.New("dependency is down")
		})
	}

	svc := health.New("test", 100*time.Millisecond,
		health.Check{Name: "postgres", Checker: checker(postgresOK)},
		health.Check{Name: "redis", Checker: checker(redisOK)},
		health.Check{Name: "kafka", Checker: checker(kafkaOK)},
	)

	apiRouter := NewRouter(NewHealthHandler(svc))
	adminMux := NewAdminMux(
		metrics.Handler(metrics.NewRegistry("test", "test")),
		NewHealthHandler(svc).LiveHandler(),
	)
	return apiRouter, adminMux
}

func readFixture(t *testing.T, name string) []map[string]string {
	t.Helper()

	path := filepath.Join("..", "..", "..", "testdata", "fixtures", name)
	f, err := os.Open(path)
	require.NoError(t, err, "fixtures must exist: %s", path)
	defer func() { _ = f.Close() }()

	reader := csv.NewReader(f)
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(records), 2)

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

func mustAtoi(t *testing.T, raw string) int {
	t.Helper()
	n := 0
	for _, r := range raw {
		require.True(t, r >= '0' && r <= '9', "not an integer: %q", raw)
		n = n*10 + int(r-'0')
	}
	return n
}
