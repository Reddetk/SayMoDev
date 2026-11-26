package health

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestService_LiveDoesNotCheckDependencies(t *testing.T) {
	svc := New("v1", time.Second, Check{
		Name:    "postgres",
		Checker: CheckerFunc(func(context.Context) error { return errors.New("down") }),
	})

	report := svc.Live()

	assert.Equal(t, "healthy", report.Status)
	assert.True(t, report.OK(), "live report has no dependency results")
	assert.Equal(t, "v1", report.Version)
	assert.WithinDuration(t, time.Now(), report.Timestamp, 2*time.Second)
}

func TestService_ReadyAllOK(t *testing.T) {
	svc := New("v1", time.Second,
		Check{Name: "postgres", Checker: CheckerFunc(func(context.Context) error { return nil })},
		Check{Name: "redis", Checker: CheckerFunc(func(context.Context) error { return nil })},
	)

	report := svc.Ready(context.Background())

	assert.Equal(t, "ready", report.Status)
	assert.Equal(t, map[string]string{"postgres": "ok", "redis": "ok"}, report.Services)
	assert.True(t, report.OK())
}

func TestService_ReadyDependencyDown(t *testing.T) {
	svc := New("v1", time.Second,
		Check{Name: "postgres", Checker: CheckerFunc(func(context.Context) error { return errors.New("boom") })},
		Check{Name: "redis", Checker: CheckerFunc(func(context.Context) error { return nil })},
	)

	report := svc.Ready(context.Background())

	assert.Equal(t, "not_ready", report.Status)
	assert.Equal(t, "error", report.Services["postgres"])
	assert.Equal(t, "ok", report.Services["redis"])
	assert.False(t, report.OK())
}

func TestService_CheckTimeout(t *testing.T) {
	svc := New("v1", 30*time.Millisecond, Check{
		Name: "kafka",
		Checker: CheckerFunc(func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				return nil
			}
		}),
	})

	start := time.Now()
	report := svc.Health(context.Background())
	elapsed := time.Since(start)

	assert.Equal(t, "degraded", report.Status)
	assert.Equal(t, "error", report.Services["kafka"])
	assert.Less(t, elapsed, time.Second, "timeout must bound the check")
}

func TestService_CheckThatIgnoresContextIsBounded(t *testing.T) {
	svc := New("v1", 20*time.Millisecond, Check{
		Name: "stuck",
		Checker: CheckerFunc(func(context.Context) error {
			time.Sleep(2 * time.Second)
			return nil
		}),
	})

	start := time.Now()
	report := svc.Ready(context.Background())
	elapsed := time.Since(start)

	assert.Equal(t, "not_ready", report.Status)
	assert.Less(t, elapsed, 500*time.Millisecond, "endpoint must not wait for a ctx-ignoring checker")
}

func TestService_CheckPanicIsRecovered(t *testing.T) {
	svc := New("v1", time.Second, Check{
		Name:    "postgres",
		Checker: CheckerFunc(func(context.Context) error { panic("driver exploded") }),
	})

	report := svc.Ready(context.Background())

	assert.Equal(t, "not_ready", report.Status)
	assert.Equal(t, "error", report.Services["postgres"])
}

func TestService_NilChecker(t *testing.T) {
	svc := New("v1", time.Second, Check{Name: "redis", Checker: nil})

	report := svc.Ready(context.Background())

	assert.Equal(t, "not_ready", report.Status)
	assert.Equal(t, "error", report.Services["redis"])
}

// Регрессия: nil-checker не должен писать в map вне синхронизации.
func TestService_NilCheckerAlongsideWorkers(t *testing.T) {
	svc := New("v1", time.Second,
		Check{Name: "postgres", Checker: CheckerFunc(func(context.Context) error { return nil })},
		Check{Name: "redis", Checker: nil},
		Check{Name: "kafka", Checker: CheckerFunc(func(context.Context) error { return nil })},
	)

	for i := 0; i < 100; i++ {
		report := svc.Ready(context.Background())
		assert.Equal(t, "not_ready", report.Status)
		assert.Equal(t, "error", report.Services["redis"])
	}
}

func TestService_ZeroTimeoutFallsBackToDefault(t *testing.T) {
	svc := New("v1", 0, Check{
		Name:    "redis",
		Checker: CheckerFunc(func(context.Context) error { return nil }),
	})

	require.NotNil(t, svc)
	assert.Equal(t, 2*time.Second, svc.timeout)
}
