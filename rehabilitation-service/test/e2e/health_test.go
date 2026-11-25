//go:build e2e

// Package e2e проверяет только HTTP-контракты запущенного сервиса.
package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func baseURL() string {
	if v := os.Getenv("E2E_BASE_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://localhost:8003"
}

func metricsURL() string {
	if v := os.Getenv("E2E_METRICS_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://localhost:9003"
}

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()

	var b strings.Builder
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	b.Write(body)
	return resp, b.String()
}

func TestHealthLiveness(t *testing.T) {
	resp, body := get(t, baseURL()+"/health")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/health: want 200, got %d (%s)", resp.StatusCode, body)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("/health body is not JSON: %v (%s)", err, body)
	}
	if payload["status"] != "ok" {
		t.Fatalf(`/health status: want "ok", got %v`, payload["status"])
	}
}

func TestReadinessAllDependencies(t *testing.T) {
	resp, body := get(t, baseURL()+"/ready")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/ready: want 200 (all deps up), got %d (%s)", resp.StatusCode, body)
	}

	var payload struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("/ready body is not JSON: %v (%s)", err, body)
	}
	if payload.Status != "ready" {
		t.Fatalf(`/ready status: want "ready", got %q`, payload.Status)
	}
	for _, dep := range []string{"postgres", "redis", "kafka", "s3"} {
		if payload.Checks[dep] != "ok" {
			t.Fatalf("/ready check %q: want ok, got %q (%s)", dep, payload.Checks[dep], body)
		}
	}
}

func TestMetricsExposesServiceUp(t *testing.T) {
	resp, body := get(t, metricsURL()+"/metrics")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/metrics: want 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(body, "service_up 1") {
		t.Fatalf("/metrics: service_up 1 not found")
	}
}

func TestMethodNotAllowed(t *testing.T) {
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(baseURL()+"/health", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /health: want 405, got %d", resp.StatusCode)
	}
}

func TestUnknownPath(t *testing.T) {
	resp, _ := get(t, baseURL()+"/healthz")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("/healthz: want 404, got %d", resp.StatusCode)
	}
}
