package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// OwnershipOrAdmin проверяет что запрашивающий является владельцем ресурса
// или имеет роль administrator.
//
// Spec: IAM §Token Validation Flow Step 4:
//   "Patient data: query with account_id filter -> empty result -> 404 (not 403)"
//   "Why 404 not 403: 403 reveals resource exists; 404 prevents enumeration (IDOR prevention)"
//
// Ожидает path-параметр :accountId в маршруте.
// Должен применяться ПОСЛЕ JWTMiddleware.
func OwnershipOrAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		authCtx := MustGetAuthContext(c)
		paramAccountID := c.Param("accountId")

		if authCtx.Role == "administrator" {
			c.Next()
			return
		}

		if authCtx.AccountID != paramAccountID {
			// 404, не 403 -- IDOR prevention согласно спецификации
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}

		c.Next()
	}
}
