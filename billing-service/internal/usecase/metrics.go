package usecase

// goca:manual — порт бизнес-метрик BC#2 (Observability.md#bc2).

import "time"

// Metrics — счётчики бизнес-событий; реализация в platform/metrics.
type Metrics interface {
	SubscriptionActivation(status string)
	PaymentResult(status string)
	WebhookReceived()
	WebhookSignatureFailure()
	WebhookLateSuccess()
	PaymentDuration(status string, seconds float64)
	SubscriptionChange(direction string)
}

// SetMetrics подключает метрики (nil допустим).
func (s *Service) SetMetrics(metrics Metrics) { s.metrics = metrics }

func (s *Service) metricWebhook() {
	if s.metrics != nil {
		s.metrics.WebhookReceived()
	}
}

func (s *Service) metricSignatureFailure() {
	if s.metrics != nil {
		s.metrics.WebhookSignatureFailure()
	}
}

func (s *Service) metricLateSuccess() {
	if s.metrics != nil {
		s.metrics.WebhookLateSuccess()
	}
}

func (s *Service) metricPaymentDuration(status string, createdAt time.Time) {
	if s.metrics == nil || createdAt.IsZero() {
		return
	}
	seconds := time.Since(createdAt).Seconds()
	if seconds < 0 {
		seconds = 0
	}
	s.metrics.PaymentDuration(status, seconds)
}

func (s *Service) metricActivation(status string) {
	if s.metrics != nil {
		s.metrics.SubscriptionActivation(status)
	}
}

func (s *Service) metricPayment(status string) {
	if s.metrics != nil {
		s.metrics.PaymentResult(status)
	}
}

func (s *Service) metricChange(direction string) {
	if s.metrics != nil {
		s.metrics.SubscriptionChange(direction)
	}
}
