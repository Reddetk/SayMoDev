// Package http — первичный HTTP-адаптер сервиса.
// goca:manual — goca handler генерирует CRUD-заготовки, а не /health,/ready,/metrics.
package http

import (
	"net/http"
	"time"

	_ "github.com/Reddetk/SayMoDev/rehabilitation-service/docs" // swagger-спека, регенерируется `swag init`
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/auth"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/health"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/platform/metrics"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/zap"
)

const ServiceName = "rehabilitation-service"

// BuildInfo — метаданные сборки, отдаются в /health и метриках.
type BuildInfo struct {
	Version   string
	Commit    string
	BuildTime string
}

// Deps — зависимости HTTP-слоя.
type Deps struct {
	Health        *health.Checker
	Metrics       *metrics.Metrics
	Logger        *zap.Logger
	Build         BuildInfo
	CORSOrigins   []string
	EnableSwagger bool
	Auth          *auth.Authenticator
	Content       *ContentHandler
}

// NewRouter собирает gin-роутер S1: health, readiness, swagger и 404/405.
func NewRouter(deps Deps) *gin.Engine {
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}

	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = discard{}
	gin.DefaultErrorWriter = discard{}

	router := gin.New()
	router.HandleMethodNotAllowed = true
	// X-Forwarded-For не доверяем: клиентский IP в логах не должен подделываться.
	_ = router.SetTrustedProxies(nil)

	// Порядок middleware: recovery → request-id/trace → заголовки → логи/метрики → CORS.
	// CORS стоит последним, но до auth: preflight обрабатывается без JWT, при этом
	// accessLog/instrument (внешние) успевают зафиксировать OPTIONS.
	router.Use(recovery(deps.Logger))
	router.Use(requestContext())
	router.Use(securityHeaders())
	router.Use(accessLog(deps.Logger))
	if deps.Metrics != nil {
		router.Use(instrument(deps.Metrics))
	}
	router.Use(cors(deps.CORSOrigins))

	router.GET("/health", healthHandler(deps))
	router.HEAD("/health", healthHandler(deps))
	router.GET("/ready", readinessHandler(deps))

	if deps.EnableSwagger {
		router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler, ginSwagger.URL("/swagger/doc.json")))
	}

	// Группы под контракты BC#3 (endpoints.md):
	//   publicRead — GET каталога/урока без JWT;
	//   adminWrite — write внутри /content, только administrator;
	//   adminOnly  — /admin/*, только administrator.
	// Наполняются в S4/S5; middleware S3 подключён заранее.
	publicRead := router.Group("/content")
	adminWrite := router.Group("/content")
	adminOnly := router.Group("/admin")
	authRead := router.Group("/content")
	// Fail-closed: без настроенного Authenticator защищённые группы отвечают 503,
	// а не становятся публичными при регистрации маршрутов в S4/S5.
	var protected []gin.HandlerFunc
	if deps.Auth != nil {
		protected = []gin.HandlerFunc{deps.Auth.Middleware(), deps.Auth.RequireAdmin()}
	} else {
		protected = []gin.HandlerFunc{denyProtected}
	}
	var authOnly []gin.HandlerFunc
	if deps.Auth != nil {
		authOnly = []gin.HandlerFunc{deps.Auth.Middleware()}
	} else {
		authOnly = []gin.HandlerFunc{denyProtected}
	}
	adminWrite.Use(protected...)
	adminOnly.Use(protected...)
	// patient GET урока: JWT обязателен, ownership проверяется через BC#4 (S4).
	authRead.Use(authOnly...)

	if deps.Content != nil {
		publicRead.GET("/programs", deps.Content.List)
		publicRead.GET("/programs/:programId", deps.Content.Get)
		authRead.GET("/programs/:programId/lessons/:lessonId", deps.Content.GetLesson)
	}
	if deps.Content != nil && deps.Content.Commands != nil {
		adminWrite.POST("/programs", deps.Content.CreateProgram)
		adminWrite.PATCH("/programs/:programId", deps.Content.UpdateProgram)
		adminWrite.DELETE("/programs/:programId", deps.Content.DeleteProgram)
		adminWrite.POST("/programs/:programId/lessons", deps.Content.AddLesson)
		adminWrite.PATCH("/programs/:programId/lessons/:lessonId", deps.Content.UpdateLesson)
		adminWrite.DELETE("/programs/:programId/lessons/:lessonId", deps.Content.DeleteLesson)
		adminOnly.POST("/content/programs/:programId/migrate-patients", deps.Content.MigratePatients)
	}

	_ = adminWrite
	_ = adminOnly

	router.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
	})
	router.NoMethod(func(c *gin.Context) {
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method_not_allowed"})
	})

	return router
}

// Health godoc
// @Summary      Liveness probe
// @Description  Returns 200 while the process is alive.
// @Tags         health
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Router       /health [get]
func healthHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":     "ok",
			"service":    ServiceName,
			"version":    deps.Build.Version,
			"commit":     deps.Build.Commit,
			"build_time": deps.Build.BuildTime,
			"timestamp":  time.Now().UTC().Format(time.RFC3339),
		})
	}
}

// Readiness godoc
// @Summary      Readiness probe
// @Description  Returns 200 only when postgres, redis, kafka and s3 are reachable; otherwise 503.
// @Tags         health
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Failure      503 {object} map[string]interface{}
// @Router       /ready [get]
func readinessHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		result := deps.Health.Check(c.Request.Context())
		status := http.StatusOK
		label := "ready"
		if !result.Ready {
			status = http.StatusServiceUnavailable
			label = "not_ready"
		}
		c.JSON(status, gin.H{
			"status": label,
			"checks": result.Checks,
		})
	}
}

// denyProtected — заглушка защищённых групп, когда Authenticator не собран
// (нет JWKS/blacklist). 503, чтобы отсутствие конфигурации не открыло write-API.
func denyProtected(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "auth_unavailable"})
}
