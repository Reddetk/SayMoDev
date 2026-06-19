package middleware

import (
	"net/http"

	"github.com/Reddetk/SayMoDev/identy-service/logger"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// OwnershipMiddleware -- gate middleware для маршрутов /iam/accounts/:accountId.
//
// Инжектируется через конструктор; не использует глобальных переменных.
// Логер обогащается trace_id/span_id из ObservabilityMiddleware через
// ContextKeyLogger -- дублирования полей нет.
type OwnershipMiddleware struct {
	logger logger.Logger
}

// NewOwnershipMiddleware создаёт OwnershipMiddleware.
// logger -- базовый логер сервиса; per-request поля добавляются из gin.Context.
func NewOwnershipMiddleware(logger logger.Logger) *OwnershipMiddleware {
	return &OwnershipMiddleware{logger: logger}
}

// Handle возвращает gin.HandlerFunc.
//
// Пропускает запрос если:
//   - role == administrator (доступ ко всем аккаунтам)
//   - token.sub == accountId (владелец видит только свои данные)
//
// IDOR prevention: не-владелец получает 404, не 403.
// Spec §Token Validation Flow Step 4.
//
// Logging: ownership_violation WARN per Observability.md при попытке
// чужого аккаунта (fields: account_id, resource_id, attempted_operation,
// trace_id, span_id).
func (m *OwnershipMiddleware) Handle() gin.HandlerFunc {
	return func(c *gin.Context) {
		ac := MustGetAuthContext(c)
		accountID := c.Param("accountId")

		if ac.Role == RoleAdministrator || ac.AccountID == accountID {
			c.Next()
			return
		}

		logger := loggerFromGinCtx(c, m.logger)
		sc := trace.SpanFromContext(c.Request.Context()).SpanContext()
		logger.Warn("ownership_violation",
			zap.String("account_id", ac.AccountID),
			zap.String("resource_id", accountID),
			zap.String("resource_type", "account"),
			zap.String("attempted_operation", c.Request.Method),
			zap.String("trace_id", sc.TraceID().String()),
			zap.String("span_id", sc.SpanID().String()),
		)

		// 404, не 403 -- IDOR prevention: не раскрываем факт существования аккаунта.
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"})
	}
}
