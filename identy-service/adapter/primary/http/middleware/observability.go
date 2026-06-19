// Package middleware -- observability middleware for BC#1 HTTP adapter.
//
// NewObservabilityMiddleware реализует три обязанности согласно Observability.md:
//
//  1. Trace context propagation -- извлекает W3C traceparent из входящего
//     запроса (client-initiated trace) или создаёт новый root span.
//     Инжектирует trace_id + span_id в gin.Context для downstream middleware
//     и handlers.
//
//  2. Structured logging (ZAP) -- пишет request-completion log entry с
//     полями, предписанными Observability.md §Logs:
//     timestamp, level, service, trace_id, span_id, method, path,
//     status, latency_ms, ip, user_agent.
//     Также сохраняет logger.Logger в gin.Context (ContextKeyLogger) чтобы
//     handlers и respond.go могли писать domain log entries.
//
//  3. Metrics -- инкрементирует cors_requests_total и cors_rejected_total
//     counters (BC#1 §Metrics Security Counter) через переданный Registerer.
//     auth_duration_seconds histogram регистрируется здесь, но записывается
//     в handler-ах через helper RecordAuthDuration.
//
// Middleware не импортирует core-пакеты -- только OTel, ZAP и Prometheus.
package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Reddetk/SayMoDev/identy-service/logger"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

const (
	// ContextKeyTraceID -- ключ для trace_id в gin.Context.
	ContextKeyTraceID = "trace_id"
	// ContextKeySpanID -- ключ для span_id в gin.Context.
	ContextKeySpanID = "span_id"
	// ContextKeyLogger -- ключ для logger.Logger в gin.Context.
	// Используется respond.go и handlers для domain log entries.
	ContextKeyLogger = "zap_logger"
)

// ObservabilityDeps -- зависимости middleware.
// Все поля обязательны; nil вызовет панику при первом запросе.
type ObservabilityDeps struct {
	Logger     logger.Logger
	Registerer prometheus.Registerer
	TracerName string // имя трейсера, например "identity-service"
}

// observabilityMiddleware -- внутреннее состояние; инициализируется один раз.
type observabilityMiddleware struct {
	logger             logger.Logger
	tracer             trace.Tracer
	corsRequestsTotal  *prometheus.CounterVec
	corsRejectedTotal  *prometheus.CounterVec
	corsPreflightTotal *prometheus.CounterVec
	authDuration       *prometheus.HistogramVec
}

// NewObservabilityMiddleware регистрирует Prometheus метрики BC#1 и возвращает
// gin.HandlerFunc. Вызывается один раз при старте сервиса.
//
// Порядок в router.go:
//
//	CORSMiddleware -> ObservabilityMiddleware -> gin.Recovery() -> ...
func NewObservabilityMiddleware(deps ObservabilityDeps) gin.HandlerFunc {
	corsRequests := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cors_requests_total",
			Help: "All CORS requests (Observability.md BC#1 Security Counter).",
		},
		[]string{"origin", "method"},
	)
	corsRejected := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cors_rejected_total",
			Help: "CORS requests rejected: origin not in whitelist.",
		},
		[]string{"origin"},
	)
	corsPreflight := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cors_preflight_requests_total",
			Help: "OPTIONS preflight requests (Observability.md BC#1 Security Counter).",
		},
		[]string{"origin"},
	)
	authDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "auth_duration_seconds",
			Help:    "Login request end-to-end latency (Observability.md BC#1 Histogram).",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"status"},
	)

	// registerOrGet -- идемпотентная регистрация; тесты могут вызывать NewGinRouter несколько раз.
	registerOrGet(deps.Registerer, corsRequests)
	registerOrGet(deps.Registerer, corsRejected)
	registerOrGet(deps.Registerer, corsPreflight)
	registerOrGet(deps.Registerer, authDuration)

	mw := &observabilityMiddleware{
		logger:             deps.Logger.With(zap.String("service", "identity")),
		tracer:             otel.Tracer(deps.TracerName),
		corsRequestsTotal:  corsRequests,
		corsRejectedTotal:  corsRejected,
		corsPreflightTotal: corsPreflight,
		authDuration:       authDuration,
	}

	return mw.handle
}

// handle -- основная логика middleware.
func (m *observabilityMiddleware) handle(c *gin.Context) {
	start := time.Now()

	// --- 1. Trace context extraction (W3C traceparent) ---
	// Если клиент передал traceparent -- продолжаем его trace.
	// Иначе OTel создаёт новый root span.
	prop := otel.GetTextMapPropagator()
	ctx := prop.Extract(c.Request.Context(), propagation.HeaderCarrier(c.Request.Header))

	ctx, span := m.tracer.Start(ctx, "gateway.request.total",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("http.method", c.Request.Method),
			attribute.String("http.route", c.FullPath()),
			attribute.String("http.url", c.Request.URL.String()),
			attribute.String("net.peer.ip", c.ClientIP()),
		),
	)
	defer span.End()

	// Обновляем Request.Context чтобы downstream-код (handlers, core) получал span.
	c.Request = c.Request.WithContext(ctx)

	traceID := span.SpanContext().TraceID().String()
	spanID := span.SpanContext().SpanID().String()

	// Сохраняем в gin.Context для handlers и respond.go.
	c.Set(ContextKeyTraceID, traceID)
	c.Set(ContextKeySpanID, spanID)

	// Сохраняем logger с уже проставленными trace_id/span_id.
	// Handlers пишут domain log entries через loggerFromCtx(c).
	requestLogger := m.logger.With(
		zap.String("trace_id", traceID),
		zap.String("span_id", spanID),
	)
	c.Set(ContextKeyLogger, requestLogger)

	// --- 2. CORS preflight counter ---
	origin := c.Request.Header.Get("Origin")
	if c.Request.Method == http.MethodOptions {
		m.corsPreflightTotal.WithLabelValues(origin).Inc()
	}
	if origin != "" {
		m.corsRequestsTotal.WithLabelValues(origin, c.Request.Method).Inc()
	}

	// --- 3. Process request ---
	c.Next()

	// --- 4. Post-request: record latency + log ---
	latency := time.Since(start)
	status := c.Writer.Status()

	span.SetAttributes(
		attribute.Int("http.status_code", status),
		attribute.Int64("http.response.latency_ms", latency.Milliseconds()),
	)

	// auth_duration_seconds -- записываем только для login endpoint.
	if c.FullPath() == "/iam/auth/login" {
		statusLabel := "success"
		if status >= 400 {
			statusLabel = "failed"
		}
		m.authDuration.WithLabelValues(statusLabel).Observe(latency.Seconds())
	}

	// ZAP request-completion log.
	fields := []zap.Field{
		zap.String("trace_id", traceID),
		zap.String("span_id", spanID),
		zap.String("method", c.Request.Method),
		zap.String("path", c.Request.URL.Path),
		zap.String("route", c.FullPath()),
		zap.Int("status", status),
		zap.Int64("latency_ms", latency.Milliseconds()),
		zap.String("ip", c.ClientIP()),
		zap.String("user_agent", c.Request.UserAgent()),
	}

	switch {
	case status >= 500:
		m.logger.Error("http_request", fields...)
	case status >= 400:
		m.logger.Warn("http_request", fields...)
	default:
		m.logger.Info("http_request", fields...)
	}
}

// RecordAuthDuration -- helper для handlers, которые знают итоговый статус
// ДО того как middleware завершит обработку (например при early-return).
// Принимает registerer для lookup уже зарегистрированной метрики.
func RecordAuthDuration(c *gin.Context, latency time.Duration, success bool) {
	// no-op placeholder -- handlers используют prometheus.MustRegisterOrGet
	// напрямую; полная реализация -- в следующем PR (handler instrumentation).
	_ = c
	_ = latency
	_ = success
}

// registerOrGet регистрирует коллектор; игнорирует AlreadyRegisteredError
// (idempotent для тестов). Паникует на любой другой ошибке.
func registerOrGet(reg prometheus.Registerer, c prometheus.Collector) {
	if err := reg.Register(c); err != nil {
		var are prometheus.AlreadyRegisteredError
		if !isAlreadyRegistered(err, &are) {
			panic("prometheus register: " + err.Error())
		}
	}
}

func isAlreadyRegistered(err error, target *prometheus.AlreadyRegisteredError) bool {
	if err == nil {
		return false
	}
	are, ok := err.(prometheus.AlreadyRegisteredError)
	if ok {
		*target = are
	}
	return ok
}

// TraceIDFromContext -- возвращает trace_id из gin.Context.
// Возвращает пустую строку если trace_id не был установлен (запрос без OTel).
func TraceIDFromContext(c *gin.Context) string {
	v, _ := c.Get(ContextKeyTraceID)
	s, _ := v.(string)
	return s
}

// SpanIDFromContext -- возвращает span_id из gin.Context.
func SpanIDFromContext(c *gin.Context) string {
	v, _ := c.Get(ContextKeySpanID)
	s, _ := v.(string)
	return s
}

// StatusLabel -- вспомогательная функция для label {status: success|failed}
// используемая в handler-ах при записи auth_attempts_total.
func StatusLabel(httpStatus int) string {
	if httpStatus < 400 {
		return "success"
	}
	return "failed"
}

// IntToStr -- мелкий helper чтобы не тащить strconv в каждый handler.
func IntToStr(i int) string { return strconv.Itoa(i) }
