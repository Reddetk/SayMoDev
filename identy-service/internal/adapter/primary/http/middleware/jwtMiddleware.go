// Package middleware stands for serios injection on data route for Gin handlers
package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	corerr "github.com/Reddetk/SayMoDev/identy-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/identy-service/internal/logger"
	inport "github.com/Reddetk/SayMoDev/identy-service/internal/port/in"
)

const AuthContextKey = "authContext"

var jwtTracer = otel.Tracer("iam.jwtMiddleware")

// jwtValidationTotal  Observability.md Metrics Security Counter:
// jwt_validation_total {result: valid|expired|invalid_signature|malformed|rev_mismatch|blacklisted}
var jwtValidationTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "jwt_validation_total",
		Help: "Total JWT validation attempts by result.",
	},
	[]string{"result"},
)

// JWTMiddleware  primary adapter.
// Validates Bearer token and writes AuthContext to c.Keys[AuthContextKey].
// Depends only on in-port TokenValidator.
//
// Error mapping (Spec Token Validation Flow):
//   - ErrJWKSKeysEmpty  > 503 Service Unavailable
//   - ErrTokenRevoked   > 401 Unauthorized
//   - all others        > 401 Unauthorized
//
// Tracing: child spans validate_jwt.* per Observability.md JWT Validation.
// Logging: zap WARN with fields reason, jti, ip, user_agent, trace_id, span_id.
type JWTMiddleware struct {
	validator inport.TokenValidator
	logger    logger.Logger
}

func NewJWTMiddleware(validator inport.TokenValidator, logger logger.Logger) *JWTMiddleware {
	return &JWTMiddleware{validator: validator, logger: logger}
}

func (m *JWTMiddleware) Handle() gin.HandlerFunc {
	return func(c *gin.Context) {
		// span: validate_jwt  parent for the full validation chain
		// Observability.md JWT Validation (per-request, every service)
		ctx, span := jwtTracer.Start(
			c.Request.Context(),
			"validate_jwt",
			trace.WithAttributes(
				attribute.String("http.method", c.Request.Method),
				attribute.String("http.path", c.FullPath()),
			),
		)
		defer span.End()
		c.Request = c.Request.WithContext(ctx)

		// span: validate_jwt.verify_signature
		_, spanSig := jwtTracer.Start(ctx, "validate_jwt.verify_signature")
		rawToken, ok := extractBearer(c)
		if !ok {
			spanSig.SetStatus(codes.Error, "missing bearer")
			spanSig.End()
			span.SetStatus(codes.Error, "missing bearer")
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				gin.H{"error": "missing or malformed Authorization header"})
			return
		}
		spanSig.End()

		// span: validate_jwt.check_exp_iat covers claims validation;
		// validate_jwt.check_blacklist and sub-spans are emitted inside ValidateToken.
		_, spanClaims := jwtTracer.Start(ctx, "validate_jwt.check_exp_iat")
		authCtx, err := m.validator.ValidateToken(c.Request.Context(), rawToken)
		if err != nil {
			spanClaims.RecordError(err)
			spanClaims.SetStatus(codes.Error, err.Error())
			spanClaims.End()
			span.RecordError(err)
			span.SetStatus(codes.Error, "token rejected")
			m.handleValidationError(c, err)
			return
		}
		spanClaims.End()

		// span: validate_jwt.rbac_check  in-memory, no IO, span required per spec
		_, spanRBAC := jwtTracer.Start(ctx, "validate_jwt.rbac_check",
			trace.WithAttributes(attribute.String("auth.role", authCtx.Role)),
		)
		spanRBAC.End()

		jwtValidationTotal.WithLabelValues("valid").Inc()
		span.SetAttributes(
			attribute.String("auth.account_id", authCtx.AccountID),
			attribute.String("auth.role", authCtx.Role),
		)
		span.SetStatus(codes.Ok, "")

		c.Set(AuthContextKey, authCtx)
		c.Next()
	}
}

// handleValidationError classifies the error, writes zap WARN with fields
// per Observability.md Logs: jwt_validation_failed, jwt_blacklist_hit, jwt_rev_mismatch.
//
// Spec Token Validation Flow:
//
//	Step 1  signature / kid errors
//	Step 2  claims (exp, iss, aud)
//	Step 3  revocation (jti blacklist, account rev)
func (m *JWTMiddleware) handleValidationError(c *gin.Context, err error) {
	sc := trace.SpanFromContext(c.Request.Context()).SpanContext()
	traceID := sc.TraceID().String()
	spanID := sc.SpanID().String()

	switch {
	case errors.Is(err, corerr.ErrJWKSKeysEmpty):
		// kid not found after re-fetch  config issue or attack. Fail-closed: 503.
		jwtValidationTotal.WithLabelValues("invalid_signature").Inc()
		m.logger.Warn("jwt_validation_failed",
			zap.String("reason", "jwks_keys_empty"),
			zap.String("ip", c.ClientIP()),
			zap.String("user_agent", c.Request.UserAgent()),
			zap.String("path", c.FullPath()),
			zap.String("trace_id", traceID),
			zap.String("span_id", spanID),
		)
		c.AbortWithStatusJSON(http.StatusServiceUnavailable,
			gin.H{"error": "authentication service unavailable"})

	case errors.Is(err, corerr.ErrTokenRevoked):
		// jti in blacklist or token.rev < account.rev
		// Observability.md: jwt_blacklist_hit WARN fields: account_id, jti, ...
		jwtValidationTotal.WithLabelValues("blacklisted").Inc()
		m.logger.Warn("jwt_validation_failed",
			zap.String("reason", "token_revoked"),
			zap.String("ip", c.ClientIP()),
			zap.String("user_agent", c.Request.UserAgent()),
			zap.String("path", c.FullPath()),
			zap.String("trace_id", traceID),
			zap.String("span_id", spanID),
		)
		c.AbortWithStatusJSON(http.StatusUnauthorized,
			gin.H{"error": "invalid or revoked token"})

	default:
		// tampered signature, expired, malformed, unknown kid  all 401
		// generic message: do not reveal reason to client
		jwtValidationTotal.WithLabelValues("invalid_signature").Inc()
		m.logger.Warn("jwt_validation_failed",
			zap.String("reason", "token_rejected"),
			zap.String("ip", c.ClientIP()),
			zap.String("user_agent", c.Request.UserAgent()),
			zap.String("path", c.FullPath()),
			zap.String("trace_id", traceID),
			zap.String("span_id", spanID),
			zap.Error(err),
		)
		c.AbortWithStatusJSON(http.StatusUnauthorized,
			gin.H{"error": "invalid or revoked token"})
	}
}

func extractBearer(c *gin.Context) (string, bool) {
	header := c.GetHeader("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return "", false
	}
	token := strings.TrimPrefix(header, "Bearer ")
	if token == "" {
		return "", false
	}
	return token, true
}
