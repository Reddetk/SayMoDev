// Package metrics — Prometheus-регистр сервиса (порт 9003).
// goca:manual — enterprise-шаблон объявляет monitoring, но не генерирует /metrics.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry *prometheus.Registry

	ServiceUp    prometheus.Gauge
	BuildInfo    *prometheus.GaugeVec
	HTTPRequests *prometheus.CounterVec
	HTTPDuration *prometheus.HistogramVec
}

func New(version, commit string) *Metrics {
	reg := prometheus.NewRegistry()

	m := &Metrics{
		registry: reg,
		ServiceUp: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "service_up",
			Help: "1 while the service process is running.",
		}),
		BuildInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "build_info",
			Help: "Build metadata; value is always 1.",
		}, []string{"version", "commit"}),
		HTTPRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "HTTP requests by method, route and status.",
		}, []string{"method", "route", "status"}),
		HTTPDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency by method and route.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
	}

	reg.MustRegister(
		m.ServiceUp,
		m.BuildInfo,
		m.HTTPRequests,
		m.HTTPDuration,
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	)

	m.BuildInfo.WithLabelValues(version, commit).Set(1)
	m.ServiceUp.Set(1)

	return m
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
