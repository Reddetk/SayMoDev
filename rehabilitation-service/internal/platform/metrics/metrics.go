// Package metrics — Prometheus-регистр сервиса (порт 9003).
// goca:manual — enterprise-шаблон объявляет monitoring, но не генерирует /metrics.
// Семейства и метки — Observability.md, раздел BC#3.
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

	// Business — counters.
	ProgramsTotal            *prometheus.CounterVec
	LessonsTotal             *prometheus.CounterVec
	ProgramVersionIncrements *prometheus.CounterVec
	MigrationTotal           *prometheus.CounterVec
	InvariantViolations      *prometheus.CounterVec
	PDFUploaded              *prometheus.CounterVec
	DeletionBlocked          *prometheus.CounterVec

	// Business — gauges.
	ProgramsCount        *prometheus.GaugeVec
	ActiveReferenceCount prometheus.Gauge
	LessonsPerProgram    *prometheus.GaugeVec

	// Infrastructure — gauges.
	CircuitBreakerState      *prometheus.GaugeVec
	KafkaConsumerQueueLength *prometheus.GaugeVec

	// Infrastructure — counters.
	CDNUploadTotal            *prometheus.CounterVec
	CircuitBreakerTransitions *prometheus.CounterVec
	RateLimitedRequests       *prometheus.CounterVec
	SingleflightCalls         *prometheus.CounterVec
	RedisWriteFailures        *prometheus.CounterVec
	CacheMissRecovery         *prometheus.CounterVec
	KafkaConsumerThrottled    *prometheus.CounterVec

	// Infrastructure — histograms.
	CDNUploadLatency    prometheus.Histogram
	ProgramQueryLatency *prometheus.HistogramVec
	SingleflightWait    *prometheus.HistogramVec
	ThrottledDelay      *prometheus.HistogramVec
	EventProcessingLag  *prometheus.HistogramVec
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

		ProgramsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "programs_total",
			Help: "Program write operations.",
		}, []string{"operation", "difficulty", "aphasia_type"}),
		LessonsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lessons_total",
			Help: "Lesson write operations.",
		}, []string{"operation"}),
		ProgramVersionIncrements: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "program_version_increments_total",
			Help: "Program version increments.",
		}, []string{"operation"}),
		MigrationTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "migration_total",
			Help: "Migration requests.",
		}, []string{"status"}),
		InvariantViolations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "invariant_violations_total",
			Help: "Domain invariant violations by invariant.",
		}, []string{"invariant"}),
		PDFUploaded: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pdf_uploaded_total",
			Help: "Uploaded PDF objects.",
		}, []string{"mime_type"}),
		DeletionBlocked: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "deletion_blocked_total",
			Help: "Deletions blocked by active references.",
		}, []string{"resource_type", "reason"}),

		ProgramsCount: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "programs_count",
			Help: "Programs by classifier.",
		}, []string{"difficulty", "aphasia_type", "is_active"}),
		ActiveReferenceCount: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "active_reference_count_total",
			Help: "Total active program references.",
		}),
		LessonsPerProgram: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lessons_per_program",
			Help: "Lessons in the current program version.",
		}, []string{"program_id"}),

		CircuitBreakerState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "circuit_breaker_state",
			Help: "Circuit breaker state: 0=closed, 1=open, 2=half_open.",
		}, []string{"service"}),
		KafkaConsumerQueueLength: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kafka_consumer_queue_length",
			Help: "Consumer lag observed for the event type.",
		}, []string{"event_type"}),

		CDNUploadTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cdn_upload_total",
			Help: "PDF upload attempts to object storage.",
		}, []string{"status"}),
		CircuitBreakerTransitions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "circuit_breaker_transitions_total",
			Help: "Circuit breaker state transitions.",
		}, []string{"service", "from", "to"}),
		RateLimitedRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "rate_limited_requests_total",
			Help: "Requests rejected by rate limiting.",
		}, []string{"operation"}),
		SingleflightCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "singleflight_calls_total",
			Help: "Singleflight call outcomes.",
		}, []string{"endpoint", "result"}),
		RedisWriteFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "redis_write_failures_total",
			Help: "Failed Redis write operations.",
		}, []string{"operation"}),
		CacheMissRecovery: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cache_miss_recovery_total",
			Help: "L1 misses recovered from L2.",
		}, []string{"key_type"}),
		KafkaConsumerThrottled: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kafka_consumer_throttled_total",
			Help: "Events delayed by consumer throttling.",
		}, []string{"event_type"}),

		CDNUploadLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "cdn_upload_latency_seconds",
			Help:    "PDF upload latency to object storage.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
		}),
		ProgramQueryLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "program_query_latency_seconds",
			Help:    "Read path latency by cache outcome.",
			Buckets: prometheus.DefBuckets,
		}, []string{"cache_hit", "singleflight_shared"}),
		SingleflightWait: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "singleflight_wait_duration_seconds",
			Help:    "Time spent inside a singleflight group.",
			Buckets: prometheus.DefBuckets,
		}, []string{"endpoint"}),
		ThrottledDelay: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "kafka_consumer_throttled_seconds",
			Help:    "Time an event waited in the consumer throttle.",
			Buckets: prometheus.DefBuckets,
		}, []string{"event_type"}),
		EventProcessingLag: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "event_processing_lag_seconds",
			Help:    "Lag between event occurrence and processing.",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 300},
		}, []string{"event_type"}),
	}

	reg.MustRegister(
		m.ServiceUp,
		m.BuildInfo,
		m.HTTPRequests,
		m.HTTPDuration,
		m.ProgramsTotal,
		m.LessonsTotal,
		m.ProgramVersionIncrements,
		m.MigrationTotal,
		m.InvariantViolations,
		m.PDFUploaded,
		m.DeletionBlocked,
		m.ProgramsCount,
		m.ActiveReferenceCount,
		m.LessonsPerProgram,
		m.CircuitBreakerState,
		m.KafkaConsumerQueueLength,
		m.CDNUploadTotal,
		m.CircuitBreakerTransitions,
		m.RateLimitedRequests,
		m.SingleflightCalls,
		m.RedisWriteFailures,
		m.CacheMissRecovery,
		m.KafkaConsumerThrottled,
		m.CDNUploadLatency,
		m.ProgramQueryLatency,
		m.SingleflightWait,
		m.ThrottledDelay,
		m.EventProcessingLag,
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	)

	m.BuildInfo.WithLabelValues(version, commit).Set(1)
	m.ServiceUp.Set(1)
	m.CircuitBreakerState.WithLabelValues("cdn").Set(0)

	return m
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
