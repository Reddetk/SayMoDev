// Package middleware stands for serios injection on data route for Gin handlers
package middleware

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
	inport "github.com/Reddetk/SayMoDev/identy-service/port/in"
)

const AuthContextKey = "authContext"

// JWTMiddleware -- primary adapter.
// Валидирует Bearer-токен и пишет AuthContext в c.Keys[AuthContextKey].
// Зависит только от in-порта TokenValidator.
//
// Маппинг ошибок (Spec §Token Validation Flow):
//   - ErrJWKSKeysEmpty  --> 503 Service Unavailable  (kid не найден после re-fetch)
//   - ErrTokenRevoked   --> 401 Unauthorized         (jti в blacklist / rev mismatch)
//   - все остальные     --> 401 Unauthorized         (tampered / expired / malformed)
//
// Структурированный лог WARN пишется для каждого типа ошибки с полями
// позволяющими корреляцию в Observability pipeline.
type JWTMiddleware struct {
	validator inport.TokenValidator
}

func NewJWTMiddleware(validator inport.TokenValidator) *JWTMiddleware {
	return &JWTMiddleware{validator: validator}
}

func (m *JWTMiddleware) Handle() gin.HandlerFunc {
	return func(c *gin.Context) {
		rawToken, ok := extractBearer(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or malformed Authorization header"})
			return
		}

		authCtx, err := m.validator.ValidateToken(c.Request.Context(), rawToken)
		if err != nil {
			m.handleValidationError(c, err)
			return
		}

		c.Set(AuthContextKey, authCtx)
		c.Next()
	}
}

// handleValidationError классифицирует ошибку валидации, пишет структурированный
// лог и завершает запрос с соответствующим HTTP-статусом.
//
// Spec §Token Validation Flow:
//   Step 1 -- signature / kid errors
//   Step 2 -- claims (exp, iss, aud)
//   Step 3 -- revocation (jti blacklist, account rev)
func (m *JWTMiddleware) handleValidationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, corerr.ErrJWKSKeysEmpty):
		// kid не найден после re-fetch JWKS -- конфигурационная проблема или
		// атака с произвольным kid. Fail-closed: 503.
		slog.WarnContext(c.Request.Context(), "jwt validation: JWKS keys empty",
			"path", c.FullPath(),
			"method", c.Request.Method,
		)
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "authentication service unavailable"})

	case errors.Is(err, corerr.ErrTokenRevoked):
		// jti в blacklist или token.rev < account.rev
		slog.WarnContext(c.Request.Context(), "jwt validation: token revoked",
			"path", c.FullPath(),
			"method", c.Request.Method,
		)
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or revoked token"})

	default:
		// tampered signature, expired, malformed, unknown kid -- всё 401
		// generic message: не раскрываем причину клиенту
		slog.WarnContext(c.Request.Context(), "jwt validation: token rejected",
			"path", c.FullPath(),
			"method", c.Request.Method,
			"error", err.Error(),
		)
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or revoked token"})
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
