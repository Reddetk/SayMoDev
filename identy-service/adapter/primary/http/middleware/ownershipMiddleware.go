package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	inport "github.com/Reddetk/SayMoDev/identy-service/port/in"
)

// OwnershipOrAdmin -- gate middleware для маршрутов /iam/accounts/:accountId.
//
// Пропускает запрос если:
//   - role == administrator (доступ ко всем аккаунтам)
//   - token.sub == accountId (владелец видит только свои данные)
//
// IDOR prevention: не-владелец получает 404, не 403.
// Spec §Token Validation Flow Step 4.
func OwnershipOrAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		ac := MustGetAuthContext(c)
		accountID := c.Param("accountId")

		if ac.Role == inport.RoleAdministrator || ac.AccountID == accountID {
			c.Next()
			return
		}

		// 404, не 403 -- IDOR prevention: не раскрываем факт существования аккаунта.
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"})
	}
}
