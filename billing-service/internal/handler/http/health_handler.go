package httpapi

// goca:manual — health-handler BC#2 не генерируется goca init.

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Reddetk/SayMoDev/billing-service/internal/platform/health"
)

// HealthHandler отдаёт /health, /health/live, /health/ready.
type HealthHandler struct {
	service *health.Service
}

// NewHealthHandler создаёт handler health-check.
func NewHealthHandler(service *health.Service) *HealthHandler {
	return &HealthHandler{service: service}
}

// Register регистрирует маршруты.
func (h *HealthHandler) Register(router gin.IRouter) {
	router.GET("/health/live", h.live)
	router.GET("/health", h.health)
	router.GET("/health/ready", h.ready)
}

// LiveHandler отдаёт liveness без проверки зависимостей (для admin-порта).
func (h *HealthHandler) LiveHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Alive"))
	})
}

func (h *HealthHandler) live(c *gin.Context) {
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte("Alive"))
}

func (h *HealthHandler) health(c *gin.Context) {
	c.JSON(http.StatusOK, h.service.Health(c.Request.Context()))
}

func (h *HealthHandler) ready(c *gin.Context) {
	report := h.service.Ready(c.Request.Context())
	status := http.StatusOK
	if !report.OK() {
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, report)
}
