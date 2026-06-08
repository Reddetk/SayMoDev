package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// rbacLogger injected via SetRBACLogger at RouterDeps construction.
// Default: zap.NewNop() -- test-safe, no nil-panic.
var rbacLogger *zap.Logger = zap.NewNop()

// SetRBACLogger is called from cmd/wire when initialising the HTTP server.
func SetRBACLogger(l *zap.Logger) { rbacLogger = l }

// RequireRole -- gate middleware: passes the request only if the role from AuthContext
// matches one of the provided roles.
//
// Panics if called without a preceding JWTMiddleware (AuthContext absent).
// This is a router configuration error, not a runtime error.
//
// Logging: ownership_violation WARN per Observability.md Logs:
//   fields: account_id, resource_id, resource_type, attempted_operation, trace_id, span_id
func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac := MustGetAuthContext(c)
		for _, r := range roles {
			if ac.Role == r {
				c.Next()
				return
			}
		}

		// Observability.md Logs: ownership_violation WARN
		sc := trace.SpanFromContext(c.Request.Context()).SpanContext()
		rbacLogger.Warn("ownership_violation",
			zap.String("account_id", ac.AccountID),
			zap.String("resource_id", c.FullPath()),
			zap.String("resource_type", "http_route"),
			zap.String("attempted_operation", c.Request.Method),
			zap.String("required_roles", joinRoles(roles)),
			zap.String("actual_role", ac.Role),
			zap.String("trace_id", sc.TraceID().String()),
			zap.String("span_id", sc.SpanID().String()),
		)

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
	}
}

func joinRoles(roles []string) string {
	if len(roles) == 0 {
		return ""
	}
	out := roles[0]
	for _, r := range roles[1:] {
		out += "|" + r
	}
	return out
}
