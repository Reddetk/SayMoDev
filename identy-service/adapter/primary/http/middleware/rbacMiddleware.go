package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequireRole возвращает middleware, который проверяет что role из AuthContext
// входит в список разрешённых ролей.
//
// При несоответствии возвращает 403 Forbidden.
//
// Должен применяться ПОСЛЕ JWTMiddleware -- использует MustGetAuthContext.
//
// Пример использования в router.go:
//
//	accounts.POST("/lock", RequireRole("administrator"), handleLockAccount(...))
func RequireRole(roles ...string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		allowed[r] = struct{}{}
	}

	return func(c *gin.Context) {
		authCtx := MustGetAuthContext(c)

		if _, ok := allowed[authCtx.Role]; !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}

		c.Next()
	}
}
