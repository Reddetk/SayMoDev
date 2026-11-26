package httpapi

// goca:manual — HTTP-спаны BC#2 (Observability.md).

import (
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// TracingMiddleware создаёт span на каждый HTTP-запрос.
func TracingMiddleware() gin.HandlerFunc {
	tracer := otel.Tracer("billing-service-http")
	return func(c *gin.Context) {
		ctx, span := tracer.Start(c.Request.Context(), c.Request.Method+" "+c.FullPath())
		defer span.End()
		c.Request = c.Request.WithContext(ctx)

		c.Next()

		span.SetAttributes(
			attribute.String("http.method", c.Request.Method),
			attribute.String("http.route", c.FullPath()),
			attribute.Int("http.status_code", c.Writer.Status()),
		)
		if c.Writer.Status() >= 500 {
			span.SetStatus(codes.Error, "internal error")
		}
	}
}
