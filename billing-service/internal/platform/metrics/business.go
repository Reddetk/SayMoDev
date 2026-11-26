package metrics

// goca:manual — бизнес-метрики BC#2 из Observability.md.

import "github.com/prometheus/client_golang/prometheus"

// Business реализует usecase.Metrics.
type Business struct {
	activation  *prometheus.CounterVec
	payments    *prometheus.CounterVec
	webhooks    *prometheus.CounterVec
	signature   prometheus.Counter
	lateSuccess prometheus.Counter
	payDuration *prometheus.HistogramVec
	changes     *prometheus.CounterVec
}

// NewBusiness регистрирует бизнес-метрики в registry.
func NewBusiness(registry *prometheus.Registry) *Business {
	business := &Business{
		activation: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "subscription_activations_total", Help: "Subscription activations by status.",
		}, []string{"status"}),
		payments: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "payment_transactions_total", Help: "Payment transactions by status.",
		}, []string{"status", "provider"}),
		webhooks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "webhook_received_total", Help: "Acquiring webhooks received.",
		}, []string{"event_type", "provider"}),
		signature: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "webhook_signature_failures_total", Help: "Webhooks with invalid HMAC or stale timestamp.",
		}),
		lateSuccess: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "webhook_late_success_total", Help: "Success webhooks for payments already failed by timeout.",
		}),
		payDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "payment_duration_seconds",
			Help:    "Time from payment creation to webhook result.",
			Buckets: prometheus.DefBuckets,
		}, []string{"status"}),
		changes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "subscription_changes_total", Help: "Subscription plan changes.",
		}, []string{"direction"}),
	}
	register(registry, business.activation, business.payments, business.webhooks, business.signature,
		business.lateSuccess, business.payDuration, business.changes)
	return business
}

// SubscriptionActivation инкрементирует счётчик активаций.
func (b *Business) SubscriptionActivation(status string) { b.activation.WithLabelValues(status).Inc() }

// PaymentResult инкрементирует счётчик платежей.
func (b *Business) PaymentResult(status string) { b.payments.WithLabelValues(status, "webpay").Inc() }

// WebhookReceived инкрементирует счётчик вебхуков.
func (b *Business) WebhookReceived() { b.webhooks.WithLabelValues("acquiring", "webpay").Inc() }

// WebhookSignatureFailure инкрементирует счётчик отклонённых подписей.
func (b *Business) WebhookSignatureFailure() { b.signature.Inc() }

// WebhookLateSuccess фиксирует success после таймаута (нужна ручная сверка).
func (b *Business) WebhookLateSuccess() { b.lateSuccess.Inc() }

// PaymentDuration фиксирует время от создания платежа до результата webhook.
func (b *Business) PaymentDuration(status string, seconds float64) {
	b.payDuration.WithLabelValues(status).Observe(seconds)
}

// SubscriptionChange инкрементирует счётчик смен тарифа.
func (b *Business) SubscriptionChange(direction string) { b.changes.WithLabelValues(direction).Inc() }
