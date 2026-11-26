package metrics

// goca:manual — метрики Postbox ACL (Observability.md: billing_postbox_*).

import "github.com/prometheus/client_golang/prometheus"

// Postbox — счётчики доставки писем.
type Postbox struct {
	sent  *prometheus.CounterVec
	error *prometheus.CounterVec
}

// NewPostbox регистрирует billing_postbox_send_total / billing_postbox_error_total.
func NewPostbox(registry *prometheus.Registry) *Postbox {
	postbox := &Postbox{
		sent: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "billing_postbox_send_total", Help: "Postbox emails sent successfully.",
		}, []string{"kind"}),
		error: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "billing_postbox_error_total", Help: "Postbox delivery errors.",
		}, []string{"kind"}),
	}
	register(registry, postbox.sent, postbox.error)
	return postbox
}

// Sent фиксирует успешную доставку.
func (p *Postbox) Sent(kind string) { p.sent.WithLabelValues(kind).Inc() }

// Error фиксирует ошибку доставки.
func (p *Postbox) Error(kind string) { p.error.WithLabelValues(kind).Inc() }
