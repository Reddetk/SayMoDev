package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Reddetk/SayMoDev/identy-service/adapter/primary/http/middleware"
	"github.com/Reddetk/SayMoDev/identy-service/logger"
)

// respondErr -- финальный fallback для всех use-case ошибок.
// Вызывается только после того как handler исчерпал все business-специфичные ветки switch.
//
// Логирует через ZAP logger, установленный ObservabilityMiddleware в gin.Context.
// Поля trace_id и span_id читаются из контекста -- они уже проставлены middleware
// и попадают в каждую запись автоматически.
func respondErr(c *gin.Context, err error) {
	logger := loggerFromCtx(c)
	traceID := middleware.TraceIDFromContext(c)
	spanID := middleware.SpanIDFromContext(c)

	switch {
	case isRateLimit(err):
		logger.Warn("rate_limit_exceeded",
			zap.String("trace_id", traceID),
			zap.String("span_id", spanID),
			zap.String("error", err.Error()),
			zap.String("ip", c.ClientIP()),
		)
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
	case isInfraError(err):
		logger.Error("infra_error",
			zap.String("trace_id", traceID),
			zap.String("span_id", spanID),
			zap.String("error", err.Error()),
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	default:
		logger.Error("unhandled_use_case_error",
			zap.String("trace_id", traceID),
			zap.String("span_id", spanID),
			zap.String("error", err.Error()),
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	}
}

// respondInternalErr -- для handler-уровневых сбоев (hashPassword, type assertions)
// когда use-case error недоступен для классификации.
func respondInternalErr(c *gin.Context) {
	logger := loggerFromCtx(c)
	logger.Error("handler_internal_error",
		zap.String("trace_id", middleware.TraceIDFromContext(c)),
		zap.String("span_id", middleware.SpanIDFromContext(c)),
		zap.String("path", c.Request.URL.Path),
	)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
}

// loggerFromCtx извлекает logger.Logger из gin.Context.
// ObservabilityMiddleware сохраняет его под ключом middleware.ContextKeyLogger.
// Если logger не найден (запрос без observability middleware -- например в тестах),
// возвращает zap.NewNop() чтобы не паниковать.
func loggerFromCtx(c *gin.Context) logger.Logger {
	v, _ := c.Get(middleware.ContextKeyLogger)

	l, _ := v.(logger.Logger)

	return l
}
