// Package metrics создаёт изолированный Prometheus registry сервиса.
package metrics

// goca:manual — изолированный Prometheus registry не генерируется goca init.

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewRegistry создаёт registry с runtime-метриками и build-info.
// Отдельный registry (не prometheus.DefaultRegisterer) нужен для чистых тестов.
func NewRegistry(version, env string) *prometheus.Registry {
	registry := prometheus.NewRegistry()
	register(registry,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "billing_build_info",
		Help: "Build information of billing-service.",
	}, []string{"version", "env"})
	buildInfo.WithLabelValues(version, env).Set(1)
	register(registry, buildInfo)

	return registry
}

// Handler отдаёт метрики в формате Prometheus text exposition.
func Handler(registry *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}
