package telemetry

// Metrics -- заглушка до добавления github.com/prometheus/client_golang в go.mod.
//
// TODO: раскомментировать и реализовать после:
//   go get github.com/prometheus/client_golang@latest
//
// Полный список метрик BC#1 (счётчики, gauge, histogram) задокументирован
// в Observability.md и согласован в design-сессии 2026-06-08.
// При реализации использовать NewMetrics(reg prometheus.Registerer) *Metrics
// с передачей registry вместо DefaultRegisterer для изоляции в тестах.
type Metrics struct{}

// NewMetrics -- заглушка. Заменить реализацией при введении prometheus.
func NewMetrics() *Metrics {
	return &Metrics{}
}
