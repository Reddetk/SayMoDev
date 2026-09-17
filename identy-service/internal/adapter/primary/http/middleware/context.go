package middleware

import (
	"github.com/gin-gonic/gin"

	inport "github.com/Reddetk/SayMoDev/identy-service/internal/port/in"
)

// GetAuthContext извлекает AuthContext из gin.Context.
// Возвращает (AuthContext, true) если контекст установлен JWTMiddleware.
// Возвращает (zero, false) если вызван на маршруте без JWTMiddleware.
func GetAuthContext(c *gin.Context) (inport.AuthContext, bool) {
	v, ok := c.Get(AuthContextKey)
	if !ok {
		return inport.AuthContext{}, false
	}
	authCtx, ok := v.(inport.AuthContext)
	return authCtx, ok
}

// MustGetAuthContext извлекает AuthContext из gin.Context.
// Паникует если контекст отсутствует  это означает, что middleware
// применён на маршруте без JWTMiddleware, что является ошибкой конфигурации.
func MustGetAuthContext(c *gin.Context) inport.AuthContext {
	authCtx, ok := GetAuthContext(c)
	if !ok {
		panic("authContext missing in gin.Context: JWTMiddleware must be applied before this handler")
	}
	return authCtx
}
