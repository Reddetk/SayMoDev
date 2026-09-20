package middleware

import (
	"net/http"
	"strings"

	"github.com/Reddetk/SayMoDev/identy-service/internal/logger"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// RBACMiddleware  gate middleware: проверяет роль из AuthContext.
//
// Инжектируется через конструктор; не использует глобальных переменных.
// Логер берётся из gin.Context[ContextKeyLogger]  обогащённый
// trace_id/span_id логер, который ObservabilityMiddleware кладёт туда
// для каждого запроса. Это исключает дублирование полей трейсинга.
type RBACMiddleware struct {
	logger logger.Logger
}

// NewRBACMiddleware создаёт RBACMiddleware.
// logger  базовый логер сервиса (без trace полей);
// per-request trace поля добавляются из gin.Context автоматически.
func NewRBACMiddleware(logger logger.Logger) *RBACMiddleware {
	return &RBACMiddleware{logger: logger}
}

// RequireRole возвращает gin.HandlerFunc, пропускающий запрос только если
// роль аккаунта совпадает с одной из roles.
//
// Logging: ownership_violation WARN per Observability.md Logs:
//
//	account_id, resource_id (FullPath), resource_type, attempted_operation,
//	required_roles, actual_role, trace_id, span_id.
//
// Паникует если вызван без предшествующего JWTMiddleware (AuthContext отсутствует).
// Это ошибка конфигурации роутера, не runtime-ошибка.
func (m *RBACMiddleware) RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac := MustGetAuthContext(c)
		for _, r := range roles {
			if ac.Role == r {
				c.Next()
				return
			}
		}

		// Берём обогащённый логер из контекста запроса.
		// ObservabilityMiddleware уже добавил trace_id/span_id в него.
		logger := loggerFromGinCtx(c, m.logger)

		sc := trace.SpanFromContext(c.Request.Context()).SpanContext()
		logger.Warn("ownership_violation",
			zap.String("account_id", ac.AccountID),
			zap.String("resource_id", c.FullPath()),
			zap.String("resource_type", "http_route"),
			zap.String("attempted_operation", c.Request.Method),
			zap.String("required_roles", strings.Join(roles, "|")),
			zap.String("actual_role", ac.Role),
			zap.String("trace_id", sc.TraceID().String()),
			zap.String("span_id", sc.SpanID().String()),
		)

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
	}
}

// loggerFromGinCtx возвращает per-request logger.Logger из gin.Context
// (положенный ObservabilityMiddleware). Если ключ отсутствует (тест без
// ObservabilityMiddleware), возвращает fallback.
func loggerFromGinCtx(c *gin.Context, fallback logger.Logger) logger.Logger {
	v, exists := c.Get(ContextKeyLogger)
	if !exists {
		return fallback
	}
	l, ok := v.(logger.Logger)
	if !ok {
		return fallback
	}
	return l
}
