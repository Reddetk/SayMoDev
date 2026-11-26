//go:build integration

// Package integration — проверки шага 1 против живого стенда.
//
// Требует запущенные зависимости и сервер (task infra:up + task run)
// либо compose. Адреса переопределяются env:
//
//	BILLING_BASE_URL     (default http://localhost:8002)
//	BILLING_METRICS_URL  (default http://localhost:9002)
package integration

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHealthEndpoints_LiveStand(t *testing.T) {
	base := envOr("BILLING_BASE_URL", "http://localhost:8002")
	metricsURL := envOr("BILLING_METRICS_URL", "http://localhost:9002")
	client := &http.Client{Timeout: 5 * time.Second}

	t.Run("live", func(t *testing.T) {
		status, body := get(t, client, base+"/health/live")
		require.Equal(t, http.StatusOK, status)
		require.Equal(t, "Alive", body)
	})

	t.Run("health", func(t *testing.T) {
		status, body := get(t, client, base+"/health")
		require.Equal(t, http.StatusOK, status)
		require.Contains(t, body, `"status":"healthy"`)
		require.Contains(t, body, `"postgres":"ok"`)
		require.Contains(t, body, `"redis":"ok"`)
		require.Contains(t, body, `"kafka":"ok"`)
	})

	t.Run("ready", func(t *testing.T) {
		status, body := get(t, client, base+"/health/ready")
		require.Equal(t, http.StatusOK, status)
		require.Contains(t, body, `"status":"ready"`)
	})

	t.Run("metrics", func(t *testing.T) {
		status, body := get(t, client, metricsURL+"/metrics")
		require.Equal(t, http.StatusOK, status)
		require.Contains(t, body, "billing_build_info")
		require.Contains(t, body, "go_goroutines")
	})

	t.Run("method_not_allowed", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, base+"/health", nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	})

	t.Run("trailing_slash_not_found", func(t *testing.T) {
		status, _ := get(t, client, base+"/health/")
		require.Equal(t, http.StatusNotFound, status)
	})
}

func get(t *testing.T, client *http.Client, url string) (int, string) {
	t.Helper()
	resp, err := client.Get(url)
	require.NoError(t, err, "stand must be reachable: %s", url)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, strings.TrimSpace(string(body))
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
