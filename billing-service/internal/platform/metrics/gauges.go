package metrics

// goca:manual — gauges/гистограммы из Observability.md#bc2.

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// SubscriptionCount — строка для gauge active_subscriptions_count.
type SubscriptionCount struct {
	Status   string
	PlanType string
	Count    int64
}

// SubscriptionGauge — custom collector: значения читаются из БД на каждом scrape.
type SubscriptionGauge struct {
	desc *prometheus.Desc
	fn   func() []SubscriptionCount
}

// NewSubscriptionGauge регистрирует collector.
func NewSubscriptionGauge(registry *prometheus.Registry, fn func() []SubscriptionCount) {
	gauge := &SubscriptionGauge{
		desc: prometheus.NewDesc("active_subscriptions_count",
			"Active/suspended subscriptions by status and plan type.", []string{"status", "plan_type"}, nil),
		fn: fn,
	}
	register(registry, gauge)
}

// Describe реализует prometheus.Collector.
func (g *SubscriptionGauge) Describe(ch chan<- *prometheus.Desc) { ch <- g.desc }

// Collect реализует prometheus.Collector.
func (g *SubscriptionGauge) Collect(ch chan<- prometheus.Metric) {
	for _, item := range g.fn() {
		ch <- prometheus.MustNewConstMetric(g.desc, prometheus.GaugeValue, float64(item.Count), item.Status, item.PlanType)
	}
}

// OutboxGauge — gauge неотправленных outbox; при ошибке БД серия не эмитится.
type OutboxGauge struct {
	desc *prometheus.Desc
	fn   func() (float64, bool)
}

// NewOutboxPendingGauge регистрирует outbox_pending_count.
func NewOutboxPendingGauge(registry *prometheus.Registry, fn func() (float64, bool)) {
	register(registry, &OutboxGauge{
		desc: prometheus.NewDesc("outbox_pending_count", "Outbox records not yet published to Kafka.", nil, nil),
		fn:   fn,
	})
}

// Describe реализует prometheus.Collector.
func (g *OutboxGauge) Describe(ch chan<- *prometheus.Desc) { ch <- g.desc }

// Collect реализует prometheus.Collector.
func (g *OutboxGauge) Collect(ch chan<- prometheus.Metric) {
	if value, ok := g.fn(); ok {
		ch <- prometheus.MustNewConstMetric(g.desc, prometheus.GaugeValue, value)
	}
}

// Acquiring — гистограмма задержки внешнего эквайринга.
type Acquiring struct {
	latency *prometheus.HistogramVec
}

// NewAcquiring регистрирует acquiring_latency_seconds.
func NewAcquiring(registry *prometheus.Registry) *Acquiring {
	histogram := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "acquiring_latency_seconds",
		Help:    "Latency of acquiring API calls.",
		Buckets: prometheus.DefBuckets,
	}, []string{"operation", "status"})
	register(registry, histogram)
	return &Acquiring{latency: histogram}
}

// Observe фиксирует длительность операции.
func (a *Acquiring) Observe(operation, status string, duration time.Duration) {
	if a == nil {
		return
	}
	a.latency.WithLabelValues(operation, status).Observe(duration.Seconds())
}
