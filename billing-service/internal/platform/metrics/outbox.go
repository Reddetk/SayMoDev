package metrics

// goca:manual — outbox-метрики BC#2 (Observability.md).

import "github.com/prometheus/client_golang/prometheus"

// Outbox — счётчики публикации outbox.
type Outbox struct {
	published *prometheus.CounterVec
	failed    *prometheus.CounterVec
}

// NewOutbox регистрирует счётчики.
func NewOutbox(registry *prometheus.Registry) *Outbox {
	outbox := &Outbox{
		published: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbox_published_total", Help: "Outbox events published to Kafka.",
		}, []string{"event_type"}),
		failed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbox_failed_total", Help: "Outbox publish failures.",
		}, []string{"event_type"}),
	}
	register(registry, outbox.published, outbox.failed)
	return outbox
}

// Published инкрементирует успешную публикацию.
func (o *Outbox) Published(eventType string) { o.published.WithLabelValues(eventType).Inc() }

// Failed инкрементирует ошибку публикации.
func (o *Outbox) Failed(eventType string) { o.failed.WithLabelValues(eventType).Inc() }
