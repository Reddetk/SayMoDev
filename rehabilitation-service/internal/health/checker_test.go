package health

import (
	"context"
	"testing"
	"time"
)

type blockingProbe struct {
	name    string
	release <-chan struct{}
}

func (p blockingProbe) Name() string { return p.name }

func (p blockingProbe) Ping(context.Context) error {
	<-p.release
	return nil
}

type okProbe struct{ name string }

func (p okProbe) Name() string               { return p.name }
func (p okProbe) Ping(context.Context) error { return nil }

// Проба, игнорирующая context, не должна вешать /ready: отдаём 503 по таймауту.
func TestCheckReturnsOnHungProbe(t *testing.T) {
	release := make(chan struct{})
	checker := NewChecker(100*time.Millisecond, blockingProbe{name: "hang", release: release})

	start := time.Now()
	result := checker.Check(context.Background())
	elapsed := time.Since(start)
	close(release)

	if elapsed > 300*time.Millisecond {
		t.Fatalf("Check blocked for %s, want <= 300ms", elapsed)
	}
	if result.Ready {
		t.Fatal("hung probe must make readiness false")
	}
	if got := result.Checks["hang"]; got != "error: timeout" {
		t.Fatalf("hung probe status: want %q, got %q", "error: timeout", got)
	}
}

func TestCheckKeepsPartialResults(t *testing.T) {
	release := make(chan struct{})
	checker := NewChecker(100*time.Millisecond,
		okProbe{name: "postgres"},
		blockingProbe{name: "kafka", release: release},
	)

	result := checker.Check(context.Background())
	close(release)

	if result.Checks["postgres"] != "ok" {
		t.Fatalf("postgres: want ok, got %q", result.Checks["postgres"])
	}
	if result.Checks["kafka"] != "error: timeout" {
		t.Fatalf("kafka: want timeout, got %q", result.Checks["kafka"])
	}
	if result.Ready {
		t.Fatal("readiness must be false when any probe timed out")
	}
}
