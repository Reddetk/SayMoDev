package httpapi

import (
	"bytes"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Reddetk/SayMoDev/progress-service/internal/adapter/telemetry"
	corerr "github.com/Reddetk/SayMoDev/progress-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/progress-service/internal/logger"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

// AuthContextKey stores the verified in.AuthContext in gin.Context.
const AuthContextKey = "authContext"

// Auth returns the JWT middleware (BC#1 Token Validation Flow).
func Auth(validator in.TokenValidator, log logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"})
			return
		}
		raw := strings.TrimPrefix(header, "Bearer ")
		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"})
			return
		}
		authCtx, err := validator.ValidateToken(c.Request.Context(), raw)
		if err != nil {
			if errors.Is(err, corerr.ErrAuthUnavailable) || errors.Is(err, corerr.ErrJWKSUnavailable) {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "auth_unavailable"})
				return
			}
			log.Warn("jwt_validation_failed", zap.String("path", c.FullPath()), zap.String("ip", c.ClientIP()))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"})
			return
		}
		c.Set(AuthContextKey, authCtx)
		c.Next()
	}
}

// RequireAdmin enforces token.role == 'administrator' for /admin/* routes.
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		authCtx, ok := CurrentAuth(c)
		if !ok || authCtx.Role != "administrator" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.Next()
	}
}

// RequireServiceToken guards the BC#3 deletion-guard endpoint (no user JWT by contract).
// The shared secret is mandatory: an unauthenticated guard would leak patient identifiers.
func RequireServiceToken(expected string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if expected == "" || c.GetHeader("X-Service-Token") != expected {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"})
			return
		}
		c.Next()
	}
}

// CurrentAuth reads the verified identity from the request context.
func CurrentAuth(c *gin.Context) (in.AuthContext, bool) {
	v, ok := c.Get(AuthContextKey)
	if !ok {
		return in.AuthContext{}, false
	}
	authCtx, ok := v.(in.AuthContext)
	return authCtx, ok
}

// AccountID returns token.sub as a UUID (patient identity == path accountId).
func AccountID(c *gin.Context) (uuid.UUID, bool) {
	authCtx, ok := CurrentAuth(c)
	if !ok {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(authCtx.AccountID)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// Idempotency implements the T1 idempotency invariant for state-mutating endpoints.
func Idempotency(store out.IdempotencyStore, ttl time.Duration, log logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("Idempotency-Key")
		if key == "" {
			badRequest(c, "idempotency_key_required")
			return
		}
		if _, err := uuid.Parse(key); err != nil {
			badRequest(c, "invalid_idempotency_key")
			return
		}
		accountID, ok := AccountID(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"})
			return
		}
		scoped := accountID.String() + ":" + key

		state, stored, err := store.Begin(c.Request.Context(), scoped, ttl)
		if err != nil {
			// Redis is the L2 cache, not the source of truth: fail-open keeps answers available
			// (PACELC PA) while the duplicate guards in PostgreSQL still hold.
			log.Warn("idempotency_store_unavailable", zap.Error(err))
			c.Next()
			return
		}
		switch state {
		case out.IdempotencyCompleted:
			if stored != nil {
				c.Data(stored.Status, "application/json; charset=utf-8", stored.Body)
				c.Abort()
				return
			}
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": "request_in_progress"})
			return
		case out.IdempotencyInProgress:
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": "request_in_progress"})
			return
		}

		rec := &responseRecorder{ResponseWriter: c.Writer, status: http.StatusOK}
		c.Writer = rec
		c.Next()

		if rec.status >= 200 && rec.status < 300 {
			if err := store.Complete(c.Request.Context(), scoped, out.StoredResponse{Status: rec.status, Body: rec.body.Bytes()}, ttl); err != nil {
				log.Warn("idempotency_store_complete_failed", zap.Error(err))
			}
			return
		}
		if err := store.Release(c.Request.Context(), scoped); err != nil {
			log.Warn("idempotency_store_release_failed", zap.Error(err))
		}
	}
}

type responseRecorder struct {
	gin.ResponseWriter
	body   bytes.Buffer
	status int
}

func (r *responseRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

// CORS runs before auth and is fail-closed for unknown origins.
func CORS(allowed []string) gin.HandlerFunc {
	allowedSet := make(map[string]bool, len(allowed))
	for _, o := range allowed {
		allowedSet[o] = true
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && allowedSet[origin] {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type,Idempotency-Key,X-Confirmation,X-Service-Token")
			c.Header("Access-Control-Max-Age", "600")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// BodyLimit caps request bodies (text answers are small; media goes directly to S3).
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil && maxBytes > 0 {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

// Recovery converts panics to the documented 500 shape and logs them.
func Recovery(log logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("panic_recovered", zap.Any("panic", rec), zap.String("path", c.FullPath()))
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
			}
		}()
		c.Next()
	}
}

// AccessLog emits one structured line per request and records request-level errors.
func AccessLog(log logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			zap.String("path", c.FullPath()),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
		}
		if len(c.Errors) > 0 {
			fields = append(fields, zap.String("error", c.Errors.String()))
		}
		if c.Writer.Status() >= 500 {
			log.Error("http_request_failed", fields...)
			return
		}
		log.Info("http_request", fields...)
	}
}

// Metrics records request counters/histograms with low-cardinality labels only.
func Metrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := strconv.Itoa(c.Writer.Status())
		telemetry.HTTPRequestsTotal.WithLabelValues(route, c.Request.Method, status).Inc()
		telemetry.HTTPRequestDuration.WithLabelValues(route, c.Request.Method).Observe(time.Since(start).Seconds())
	}
}
