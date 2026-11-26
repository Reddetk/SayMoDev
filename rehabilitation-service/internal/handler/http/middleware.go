package http

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/platform/metrics"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// tracing создаёт OTel-спан на HTTP-запрос; trace_id уходит в логи и заголовок.
func tracing(service string) gin.HandlerFunc {
	tracer := otel.Tracer(service + "/http")
	return func(c *gin.Context) {
		ctx, span := tracer.Start(c.Request.Context(), c.Request.Method+" "+c.FullPath())
		defer span.End()
		c.Request = c.Request.WithContext(ctx)
		c.Next()

		span.SetAttributes(
			attribute.String("http.method", c.Request.Method),
			attribute.String("http.path", c.Request.URL.Path),
			attribute.Int("http.status_code", c.Writer.Status()),
		)
	}
}

func recovery(log *zap.Logger) gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, recovered any) {
		log.Error("panic recovered",
			zap.Any("panic", recovered),
			zap.String("path", c.Request.URL.Path),
			zap.String("request_id", c.GetString("request_id")),
			zap.String("trace_id", c.GetString("trace_id")),
		)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
	})
}

const (
	headerRequestID = "X-Request-ID"
	headerTraceID   = "X-Trace-ID"
)

// requestContext проставляет request_id и trace_id. trace_id берётся из входящего
// W3C traceparent (если валиден), иначе генерируется: контракт S1.5 требует
// корреляцию логов без подключения OTel-экспортёра (экспорт — S8).
func requestContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader(headerRequestID)
		if requestID == "" {
			requestID = uuid.NewString()
		}
		c.Set("request_id", requestID)
		c.Header(headerRequestID, requestID)

		traceID := traceIDFromTraceparent(c.GetHeader("traceparent"))
		if span := trace.SpanFromContext(c.Request.Context()); span.SpanContext().IsValid() {
			traceID = span.SpanContext().TraceID().String()
		}
		if traceID == "" {
			traceID = randomTraceID()
		}
		c.Set("trace_id", traceID)
		c.Header(headerTraceID, traceID)

		c.Next()
	}
}

func cors(allowed []string) gin.HandlerFunc {
	allowAny := false
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, origin := range allowed {
		if origin == "*" {
			allowAny = true
		}
		allowedSet[origin] = struct{}{}
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			if _, ok := allowedSet[origin]; ok || allowAny {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Header("Vary", "Origin")
				c.Header("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,OPTIONS")
				c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Request-ID,Idempotency-Key,traceparent")
				c.Header("Access-Control-Expose-Headers", headerRequestID+","+headerTraceID)
				c.Header("Access-Control-Max-Age", "600")
			}
		}

		// Preflight не несёт Authorization: обрабатывается до JWT/RBAC (S3).
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		c.Next()
	}
}

func accessLog(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		log.Info("http_request",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
			zap.String("request_id", c.GetString("request_id")),
			zap.String("trace_id", c.GetString("trace_id")),
			zap.String("client_ip", c.ClientIP()),
		)
	}
}

func instrument(m *metrics.Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unknown"
		}
		status := strconv.Itoa(c.Writer.Status())
		m.HTTPRequests.WithLabelValues(c.Request.Method, route, status).Inc()
		m.HTTPDuration.WithLabelValues(c.Request.Method, route).Observe(time.Since(start).Seconds())
	}
}

// traceIDFromTraceparent разбирает W3C traceparent: 00-<32hex>-<16hex>-<flags>.
func traceIDFromTraceparent(value string) string {
	parts := strings.Split(value, "-")
	if len(parts) < 4 || len(parts[1]) != 32 {
		return ""
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return ""
	}
	if parts[1] == strings.Repeat("0", 32) {
		return ""
	}
	return parts[1]
}

func randomTraceID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uuid.NewString()
	}
	return hex.EncodeToString(b[:])
}
