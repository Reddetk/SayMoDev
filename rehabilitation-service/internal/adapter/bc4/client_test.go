package bc4

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestActivePatientsContract(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"activeCount":3}`))
	}))
	defer server.Close()

	client := New(server.URL)
	count, err := client.ActivePatients(context.Background(), "p1", 2)
	if err != nil || count != 3 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if path != "/admin/progress/program/p1/version/2/active-patients" {
		t.Fatalf("path: %s", path)
	}
}

func TestActivePatientsAcceptsCountField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"count":2}`))
	}))
	defer server.Close()

	count, err := New(server.URL).ActivePatients(context.Background(), "p1", 1)
	if err != nil || count != 2 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestCircuitBreakerOpensAndFailsClosed(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := New(server.URL)
	for i := 0; i < 5; i++ {
		if _, err := client.ActivePatients(context.Background(), "p1", 1); err == nil {
			t.Fatalf("failure %d: want error", i+1)
		}
	}
	if _, err := client.ActivePatients(context.Background(), "p1", 1); err == nil || !strings.Contains(err.Error(), "breaker") {
		t.Fatalf("want open breaker, got %v", err)
	}
	if calls != 5 {
		t.Fatalf("server calls: want 5, got %d", calls)
	}
}
