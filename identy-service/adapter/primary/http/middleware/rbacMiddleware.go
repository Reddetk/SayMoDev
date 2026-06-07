package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequireRole -- gate middleware: пропускает запрос только если роль из AuthContext
// совпадает с одной из переданных roles.
//
// Паникует если вызван без предшествующего JWTMiddleware (AuthContext отсутствует).
// Это ошибка конфигурации роутера, не runtime-ошибка.
func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac := MustGetAuthContext(c)
		for _, r := range roles {
			if ac.Role == r {
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
	}
}
