// Package middleware stands for serios injection on data route for Gin handlers
package middleware

import (
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
// Маппинг ошибок:
//   - ErrTokenRevoked  --> 401 Unauthorized
//   - ErrJWKSKeysEmpty --> 503 Service Unavailable
//   - отсутствие / неверный формат --> 401
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
			if err == corerr.ErrJWKSKeysEmpty {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "authentication service unavailable"})
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or revoked token"})
			return
		}

		c.Set(AuthContextKey, authCtx)
		c.Next()
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
