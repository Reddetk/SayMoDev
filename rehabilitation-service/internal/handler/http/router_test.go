package http

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

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/health"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/platform/metrics"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type fakeProbe struct {
	name string
	err  error
}

func (p fakeProbe) Name() string { return p.name }

func (p fakeProbe) Ping(context.Context) error { return p.err }

func readCSV(t *testing.T, name string) ([]map[string]string, map[string]int) {
	t.Helper()
	path := filepath.Join("..", "..", "..", "testdata", "fixtures", name)
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixtures: %v", err)
	}
	defer f.Close()

	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}

	header := map[string]int{}
	for i, h := range rows[0] {
		header[h] = i
	}

	out := make([]map[string]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		rec := map[string]string{}
		for name, idx := range header {
			rec[name] = row[idx]
		}
		out = append(out, rec)
	}
	return out, header
}

func newTestRouter(probes ...health.Probe) http.Handler {
	return NewRouter(Deps{
		Health:      health.NewChecker(0, probes...),
		Metrics:     metrics.New("test", "test"),
		Build:       BuildInfo{Version: "test", Commit: "test", BuildTime: "test"},
		CORSOrigins: []string{"http://localhost:3000"},
	})
}

func TestCORSAllowsConfiguredOriginBeforeAuth(t *testing.T) {
	router := newTestRouter()

	req := httptest.NewRequest(http.MethodOptions, "/health", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight: want 204, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("allow-origin: want configured origin, got %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "http://evil.example")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unknown origin must not receive allow-origin, got %q", got)
	}
}

func TestDenyProtectedWithoutAuthReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	denyProtected(c)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rec.Code)
	}
}

func TestMigrationRateLimiter(t *testing.T) {
	var limiter migrationLimiter
	now := time.Now()
	for i := 0; i < 10; i++ {
		if !limiter.allow("admin", 10, time.Minute, now) {
			t.Fatalf("request %d must be allowed", i+1)
		}
	}
	if limiter.allow("admin", 10, time.Minute, now) {
		t.Fatal("11th request within a minute must be rejected")
	}
	if !limiter.allow("other", 10, time.Minute, now) {
		t.Fatal("limit is per admin")
	}
	if !limiter.allow("admin", 10, time.Minute, now.Add(time.Minute)) {
		t.Fatal("new window must allow")
	}
}

func TestTraceIDFromTraceparent(t *testing.T) {
	router := newTestRouter()
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Trace-ID"); got != traceID {
		t.Fatalf("X-Trace-ID: want %q, got %q", traceID, got)
	}
}

func TestTraceparentPropagatesToSpan(t *testing.T) {
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})

	router := newTestRouter()
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// С включённым провайдером X-Trace-ID берётся из span context: он обязан
	// совпасть с входящим traceparent, иначе BC#3 начинает новый root-трейс.
	if got := rec.Header().Get("X-Trace-ID"); got != traceID {
		t.Fatalf("X-Trace-ID: want inbound %q, got %q", traceID, got)
	}
}

func TestHealthFixtures(t *testing.T) {
	cases, _ := readCSV(t, "health.csv")
	router := newTestRouter()

	for _, tc := range cases {
		tc := tc
		t.Run(tc["case_id"], func(t *testing.T) {
			req := httptest.NewRequest(tc["method"], tc["path"], nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			want := atoi(t, tc["expected_status"])
			if rec.Code != want {
				t.Fatalf("status: want %d, got %d (body %s)", want, rec.Code, rec.Body.String())
			}
			if contains := tc["expected_body_contains"]; contains != "" {
				if !strings.Contains(rec.Body.String(), contains) {
					t.Fatalf("body %q does not contain %q", rec.Body.String(), contains)
				}
			}
		})
	}
}

func TestReadyFixtures(t *testing.T) {
	cases, _ := readCSV(t, "ready.csv")

	for _, tc := range cases {
		tc := tc
		t.Run(tc["case_id"], func(t *testing.T) {
			mk := func(name string) health.Probe {
				if tc[name+"_available"] == "true" {
					return fakeProbe{name: name}
				}
				return fakeProbe{name: name, err: errors.New(name + " unavailable")}
			}
			router := newTestRouter(mk("postgres"), mk("redis"), mk("kafka"), mk("s3"))

			req := httptest.NewRequest(http.MethodGet, "/ready", nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			want := atoi(t, tc["expected_status"])
			if rec.Code != want {
				t.Fatalf("status: want %d, got %d (body %s)", want, rec.Code, rec.Body.String())
			}
			if contains := tc["expected_body_contains"]; contains != "" {
				if !strings.Contains(rec.Body.String(), contains) {
					t.Fatalf("body %q does not contain %q", rec.Body.String(), contains)
				}
			}
		})
	}
}

func TestMetricsEndpointExposesServiceUp(t *testing.T) {
	m := metrics.New("test", "test")
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status: want 200, got %d", rec.Code)
	}
	for _, want := range []string{"service_up 1", "build_info"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("metrics body missing %q", want)
		}
	}
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			t.Fatalf("not a number: %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n
}
