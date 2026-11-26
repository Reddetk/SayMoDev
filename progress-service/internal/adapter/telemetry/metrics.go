// Package telemetry registers BC#4 Prometheus metrics and OTel tracing.
package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// HTTPRequestsTotal counts every HTTP request by route (never by account_id).
	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total", Help: "HTTP requests by route, method and status.",
	}, []string{"route", "method", "status"})

	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "http_request_duration_seconds", Help: "HTTP request latency.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route", "method"})

	ProgramsActivatedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "programs_activated_total", Help: "Program activations (event-driven, fallback and resume).",
	})
	LessonsAnswersSubmittedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "lessons_answers_submitted_total", Help: "Lesson answers submitted by media type.",
	}, []string{"media_type"})
	LessonsCompletedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "lessons_completed_total", Help: "Explicit lesson completions.",
	})
	ProgramsCompletedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "programs_completed_total", Help: "Programs reaching 100%.",
	})
	LessonCompletionRejectedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "lesson_completion_rejected_total", Help: "Rejected completions by reason.",
	}, []string{"reason"})
	AccessRecalculationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "access_recalculations_total", Help: "Access recalculations by trigger.",
	}, []string{"trigger"})
	BC3APICallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "bc3_api_calls_total", Help: "Calls to BC#3 catalog by endpoint and status.",
	}, []string{"endpoint", "status"})
	RedisWriteFailuresTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "redis_write_failures_total", Help: "Failed Redis writes by operation.",
	}, []string{"operation"})
	ActiveProgramsGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "active_programs_count", Help: "Number of programs currently active (gauge updated by polling).",
	})
	LessonAnswerSubmissionLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "lesson_answer_submission_latency_seconds", Help: "Answer submission latency.",
		Buckets: prometheus.DefBuckets,
	})
	LessonCompletionLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "lesson_completion_latency_seconds", Help: "Lesson completion latency.",
		Buckets: prometheus.DefBuckets,
	})
	BC3APILatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "bc3_api_latency_seconds", Help: "BC#3 sync call latency.",
		Buckets: prometheus.DefBuckets,
	}, []string{"endpoint"})
	EventProcessingLag = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "event_processing_lag_seconds", Help: "Kafka event processing lag.",
		Buckets: prometheus.DefBuckets,
	}, []string{"event_type"})
	AccessRecalculationLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "access_recalculation_latency_seconds", Help: "Access recalculation latency.",
		Buckets: prometheus.DefBuckets,
	})
	JWKSActiveKeys = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "jwks_active_keys_count", Help: "Number of active IAM JWKS keys.",
	})
)
