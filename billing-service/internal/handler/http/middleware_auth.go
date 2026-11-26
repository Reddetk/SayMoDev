package httpapi

// goca:manual — JWT/account-status/revocation middleware BC#2 (T1[1.1–1.2]).

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Reddetk/SayMoDev/billing-service/internal/adapter/auth"
	"github.com/Reddetk/SayMoDev/billing-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/billing-service/internal/port/out"
)

const authContextKey = "billingAuth"

// AuthMiddleware валидирует JWT, статус аккаунта и отзыв токена.
func AuthMiddleware(verifier *auth.Verifier, store out.Store, cache out.Cache) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			abortJSON(c, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		claims, err := verifier.Verify(c.Request.Context(), strings.TrimPrefix(header, "Bearer "))
		if err != nil {
			abortJSON(c, http.StatusUnauthorized, "unauthorized", "invalid token")
			return
		}
		if _, err := uuid.Parse(claims.Subject); err != nil {
			abortJSON(c, http.StatusUnauthorized, "unauthorized", "invalid token subject")
			return
		}

		status, err := store.GetAccountStatus(c.Request.Context(), claims.Subject)
		if err != nil && !errors.Is(err, out.ErrNotFound) {
			abortJSON(c, http.StatusServiceUnavailable, "internal_error", "account status unavailable")
			return
		}
		if status == "locked" || status == "deleted" {
			abortJSON(c, http.StatusForbidden, "account_inactive", "account is not active")
			return
		}

		revoked, err := cache.IsRevoked(c.Request.Context(), claims.JTI, claims.Subject, claims.Rev)
		if err != nil {
			abortJSON(c, http.StatusServiceUnavailable, "internal_error", "revocation store unavailable")
			return
		}
		if revoked {
			abortJSON(c, http.StatusUnauthorized, "unauthorized", "token revoked")
			return
		}

		c.Set(authContextKey, in.AuthContext{
			AccountID: claims.Subject,
			Role:      claims.Role,
			SessionID: claims.SessionID,
			Rev:       claims.Rev,
		})
		c.Next()
	}
}

func authFrom(c *gin.Context) in.AuthContext {
	value, _ := c.Get(authContextKey)
	authCtx, _ := value.(in.AuthContext)
	return authCtx
}

func abortJSON(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": code, "message": message})
}
