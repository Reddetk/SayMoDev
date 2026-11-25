// Package health агрегирует проверки зависимостей для /ready.
// goca:manual — генератор не создаёт readiness-агрегатор с параллельными пробами.
package health

import (
	"context"
	"time"
)

// Probe — зависимость, готовность которой проверяется.
type Probe interface {
	Name() string
	Ping(ctx context.Context) error
}

// Result — итог проверки готовности.
type Result struct {
	Ready  bool              `json:"ready"`
	Checks map[string]string `json:"checks"`
}

// Checker параллельно опрашивает пробы.
//
// Жёсткая граница: ответ формируется не позже timeout+50ms, даже если адаптер
// игнорирует context (например, зависшее TCP-соединение). Незавершённые пробы
// получают статус "error: timeout" — это fail-closed, а не вечное ожидание.
type Checker struct {
	probes  []Probe
	timeout time.Duration
}

func NewChecker(timeout time.Duration, probes ...Probe) *Checker {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &Checker{probes: probes, timeout: timeout}
}

type probeResult struct {
	name   string
	status string
}

// Check возвращает готовность всех зависимостей. Ошибка одной пробы не отменяет остальные.
func (c *Checker) Check(ctx context.Context) Result {
	checks := make(map[string]string, len(c.probes))
	names := make([]string, 0, len(c.probes))
	results := make(chan probeResult, len(c.probes))

	for _, probe := range c.probes {
		names = append(names, probe.Name())
		go func(p Probe) {
			probeCtx, cancel := context.WithTimeout(ctx, c.timeout)
			defer cancel()

			status := "ok"
			if err := p.Ping(probeCtx); err != nil {
				status = "error: " + err.Error()
			}
			results <- probeResult{name: p.Name(), status: status}
		}(probe)
	}

	timer := time.NewTimer(c.timeout + 50*time.Millisecond)
	defer timer.Stop()

collect:
	for range c.probes {
		select {
		case r := <-results:
			checks[r.name] = r.status
		case <-timer.C:
			for _, name := range names {
				if _, ok := checks[name]; !ok {
					checks[name] = "error: timeout"
				}
			}
			break collect
		}
	}

	ready := true
	for _, status := range checks {
		if status != "ok" {
			ready = false
			break
		}
	}

	return Result{Ready: ready, Checks: checks}
}
