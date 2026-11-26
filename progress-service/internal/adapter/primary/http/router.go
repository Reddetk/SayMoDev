package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"github.com/Reddetk/SayMoDev/progress-service/internal/adapter/telemetry"
	"github.com/Reddetk/SayMoDev/progress-service/internal/config"
	corerr "github.com/Reddetk/SayMoDev/progress-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/progress-service/internal/logger"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

// Handlers holds all HTTP use-case ports.
type Handlers struct {
	progress  in.ProgressReader
	programs  in.ProgramOperator
	answers   in.LessonAnswerer
	completer in.LessonCompleter
	deleter   in.SelfDeleter
	admin     in.AdminReader
	adminOp   in.AdminOperator
	guard     in.DeletionGuard
}

// Pinger is a readiness dependency (postgres, redis, blacklist).
type Pinger interface {
	Ping(ctx context.Context) error
}

// HealthDeps are the readiness dependencies.
type HealthDeps struct {
	Postgres  Pinger
	Cache     out.Cache
	Blacklist out.RevocationStore
}

// Deps is everything the primary adapter needs.
type Deps struct {
	Config    *config.Config
	Log       logger.Logger
	Validator in.TokenValidator
	Idem      out.IdempotencyStore
	Progress  in.ProgressReader
	Programs  in.ProgramOperator
	Answers   in.LessonAnswerer
	Completer in.LessonCompleter
	Deleter   in.SelfDeleter
	Admin     in.AdminReader
	AdminOp   in.AdminOperator
	Guard     in.DeletionGuard
	Health    HealthDeps
}

// NewRouter builds the Gin engine with the documented middleware chain.
func NewRouter(d Deps) *gin.Engine {
	if d.Config.Environment == "prod" {
		gin.SetMode(gin.ReleaseMode)
	}
	engine := gin.New()
	engine.Use(Recovery(d.Log), CORS(d.Config.CORSAllowedOrigins), AccessLog(d.Log), Metrics(), BodyLimit(d.Config.MaxBodyBytes))

	h := &Handlers{
		progress: d.Progress, programs: d.Programs, answers: d.Answers,
		completer: d.Completer, deleter: d.Deleter, admin: d.Admin, adminOp: d.AdminOp, guard: d.Guard,
	}

	engine.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "healthy", "service": "progress-service", "timestamp": time.Now().UTC()})
	})
	engine.GET("/health/live", func(c *gin.Context) { c.String(http.StatusOK, "Alive") })
	engine.GET("/health/ready", readiness(d.Health))
	engine.GET("/metrics", gin.WrapH(promhttp.Handler()))
	engine.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	auth := Auth(d.Validator, d.Log)
	idem := Idempotency(d.Idem, d.Config.IdempotencyTTL, d.Log)

	patient := engine.Group("")
	patient.Use(auth)
	{
		patient.GET("/progress/me", h.GetMyProgress)
		patient.POST("/progress/me/activate", idem, h.ActivateProgram)
		patient.DELETE("/progress/me", h.DeleteMyProgress)
		patient.POST("/progress/lessons/:lessonId/answers/presign", h.PresignAnswer)
		patient.POST("/progress/lessons/:lessonId/answers", idem, h.SubmitAnswer)
		patient.POST("/progress/lessons/:lessonId/complete", idem, h.CompleteLesson)
		patient.PATCH("/progress/difficulty", idem, h.ChangeDifficulty)
		patient.POST("/progress/programs/:programId/resume", idem, h.ResumeProgram)
	}

	// Deletion guard: service-to-service, no user JWT (endpoints.md#bc4).
	engine.GET("/admin/progress/program/:programId/version/:version/active-patients",
		RequireServiceToken(d.Config.InternalServiceToken), h.ActivePatients)

	admin := engine.Group("/admin")
	admin.Use(auth, RequireAdmin())
	{
		admin.GET("/progress/:accountId", h.AdminGetProgress)
		admin.PATCH("/progress/:accountId/difficulty", h.AdminChangeDifficulty)
		admin.GET("/answers/:accountId", h.AdminListAnswers)
		admin.GET("/answers/:accountId/:lessonId", h.AdminLessonAnswers)
	}

	return engine
}

func readiness(h HealthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		services := gin.H{}
		ok := true
		if h.Postgres != nil {
			if err := h.Postgres.Ping(ctx); err != nil {
				services["postgres"] = "error: " + err.Error()
				ok = false
			} else {
				services["postgres"] = "healthy"
			}
		}
		if h.Cache != nil {
			if err := h.Cache.Ping(ctx); err != nil {
				services["redis"] = "error: " + err.Error()
				ok = false
			} else {
				services["redis"] = "healthy"
			}
		}
		if h.Blacklist != nil {
			if err := h.Blacklist.Ping(ctx); err != nil {
				services["redis_blacklist"] = "error: " + err.Error()
				ok = false
			} else {
				services["redis_blacklist"] = "healthy"
			}
		}
		if !ok {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "services": services})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready", "services": services})
	}
}

// recordCompletionRejection keeps lesson_completion_rejected_total reason-labelled.
func recordCompletionRejection(err error) {
	switch {
	case errors.Is(err, corerr.ErrLessonAlreadyCompleted):
		telemetry.LessonCompletionRejectedTotal.WithLabelValues("already_completed").Inc()
	case errors.Is(err, corerr.ErrNoAnswerSubmitted):
		telemetry.LessonCompletionRejectedTotal.WithLabelValues("no_answer").Inc()
	case errors.Is(err, corerr.ErrLessonNotAccessible):
		telemetry.LessonCompletionRejectedTotal.WithLabelValues("not_accessible").Inc()
	}
}
