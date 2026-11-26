// Package health — минимальный health-check каркаса сервиса.
//
// Разделение намеренное:
//   - /health/live  — процесс жив, зависимости не проверяются;
//   - /health       — агрегированный отчёт, всегда 200 (для дашбордов);
//   - /health/ready — 200 только если все зависимости доступны (для k8s readiness).
package health

// goca:manual — health-контракт BC#2 (live/health/ready) не генерируется goca init.

import (
	"context"
	"fmt"
	"time"
)

// Checker проверяет одну зависимость.
type Checker interface {
	Check(ctx context.Context) error
}

// CheckerFunc адаптирует функцию к Checker.
type CheckerFunc func(ctx context.Context) error

// Check выполняет функцию.
func (f CheckerFunc) Check(ctx context.Context) error { return f(ctx) }

// Check — именованная зависимость.
type Check struct {
	Name    string
	Checker Checker
}

// Report — результат проверки. Services: "ok" | "error".
type Report struct {
	Status    string            `json:"status"`
	Timestamp time.Time         `json:"timestamp"`
	Version   string            `json:"version"`
	Services  map[string]string `json:"services"`
}

// OK сообщает, все ли зависимости доступны.
func (r Report) OK() bool {
	for _, status := range r.Services {
		if status != "ok" {
			return false
		}
	}
	return true
}

// Service выполняет проверки с общим таймаутом.
type Service struct {
	version string
	timeout time.Duration
	checks  []Check
	now     func() time.Time
}

// New создаёт health-сервис.
func New(version string, timeout time.Duration, checks ...Check) *Service {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &Service{
		version: version,
		timeout: timeout,
		checks:  checks,
		now:     time.Now,
	}
}

// Live возвращает отчёт без проверки зависимостей.
func (s *Service) Live() Report {
	return Report{
		Status:    "healthy",
		Timestamp: s.now().UTC(),
		Version:   s.version,
		Services:  map[string]string{},
	}
}

// Health проверяет зависимости и всегда возвращает статус healthy|degraded.
func (s *Service) Health(ctx context.Context) Report {
	report := s.evaluate(ctx)
	if report.OK() {
		report.Status = "healthy"
	} else {
		report.Status = "degraded"
	}
	return report
}

// Ready проверяет зависимости и возвращает статус ready|not_ready.
func (s *Service) Ready(ctx context.Context) Report {
	report := s.evaluate(ctx)
	if report.OK() {
		report.Status = "ready"
	} else {
		report.Status = "not_ready"
	}
	return report
}

type checkResult struct {
	name   string
	status string
}

func (s *Service) evaluate(ctx context.Context) Report {
	report := Report{
		Timestamp: s.now().UTC(),
		Version:   s.version,
		Services:  make(map[string]string, len(s.checks)),
	}

	// Буферизованный канал: даже если checker игнорирует ctx, горутина
	// завершится и не заблокируется на отправке результата.
	results := make(chan checkResult, len(s.checks))
	for _, check := range s.checks {
		if check.Checker == nil {
			results <- checkResult{name: check.Name, status: "error"}
			continue
		}
		go func(c Check) {
			status := "ok"
			if err := runCheck(ctx, s.timeout, c.Checker); err != nil {
				status = "error"
			}
			results <- checkResult{name: c.Name, status: status}
		}(check)
	}

	for range s.checks {
		result := <-results
		report.Services[result.name] = result.status
	}

	return report
}

func runCheck(ctx context.Context, timeout time.Duration, checker Checker) error {
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		var err error
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("health check panic: %v", recovered)
			}
			done <- err
		}()
		err = checker.Check(checkCtx)
	}()

	select {
	case err := <-done:
		return err
	case <-checkCtx.Done():
		// Таймаут/отмена родительского контекста: не ждём checker, который
		// проигнорировал ctx. Горутина завершится сама (канал буферизован);
		// checker, который не завершится никогда, оставит одну горутину на вызов —
		// цена завёрнутого в таймаут endpoint'а.
		return checkCtx.Err()
	}
}
