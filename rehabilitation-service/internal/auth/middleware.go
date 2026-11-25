// goca:manual — 401/403 middleware и fail-closed composition for BC#3.
package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const identityContextKey = "auth.identity"

// Middleware требует валидный Bearer JWT. 401 при любой ошибке (fail-closed).
func (a *Authenticator) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			unauthorized(c, ErrMissingToken)
			return
		}
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
			unauthorized(c, ErrMalformed)
			return
		}

		identity, err := a.Authenticate(c.Request.Context(), parts[1])
		if err != nil {
			unauthorized(c, err)
			return
		}

		c.Set(identityContextKey, identity)
		c.Next()
	}
}

// RequireAdmin пропускает только role=administrator; 403 для валидного токена без прав.
func (a *Authenticator) RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		value, ok := c.Get(identityContextKey)
		if !ok {
			unauthorized(c, ErrMissingToken)
			return
		}
		identity, ok := value.(Identity)
		if !ok {
			unauthorized(c, ErrClaims)
			return
		}
		if identity.Role != "administrator" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.Next()
	}
}

func unauthorized(c *gin.Context, err error) {
	c.Header("WWW-Authenticate", `Bearer error="invalid_token"`)
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"error": "unauthorized",
		"code":  authErrorCode(err),
	})
}

// authErrorCode отдаёт стабильный код причины (без сети/URL/топологии).
func authErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrExpired):
		return "expired"
	case errors.Is(err, ErrIssuer):
		return "issuer"
	case errors.Is(err, ErrAudience):
		return "audience"
	case errors.Is(err, ErrSignature):
		return "signature"
	case errors.Is(err, ErrKID):
		return "kid"
	case errors.Is(err, ErrAlg):
		return "alg"
	case errors.Is(err, ErrJWKS):
		return "jwks"
	case errors.Is(err, ErrRevoked):
		return "revoked"
	case errors.Is(err, ErrRev):
		return "rev"
	case errors.Is(err, ErrRevocationUnavailable):
		return "revocation_unavailable"
	case errors.Is(err, ErrClaims):
		return "claims"
	default:
		return "unauthorized"
	}
}
