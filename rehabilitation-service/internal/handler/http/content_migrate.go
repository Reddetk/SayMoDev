package http

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/auth"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/port/out"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/usecase"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// migrationLimiter — 10 запросов/минуту на администратора (in-process).
type migrationLimiter struct {
	mu     sync.Mutex
	window map[string]limiterWindow
}

type limiterWindow struct {
	start time.Time
	count int
}

func (l *migrationLimiter) allow(key string, limit int, window time.Duration, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.window == nil {
		l.window = map[string]limiterWindow{}
	}
	w, ok := l.window[key]
	if !ok || now.Sub(w.start) >= window {
		l.window[key] = limiterWindow{start: now, count: 1}
		return true
	}
	if w.count >= limit {
		return false
	}
	w.count++
	l.window[key] = w
	return true
}

type migrateRequest struct {
	TargetVersion    int      `json:"target_version"`
	PatientSelection string   `json:"patient_selection"`
	AccountIDs       []string `json:"account_ids"`
}

// MigratePatients godoc
// @Summary      Request batch migration of patients to a program version
// @Tags         content
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        programId path string true "program UUID"
// @Success      202 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Failure      429 {object} map[string]interface{}
// @Router       /admin/content/programs/{programId}/migrate-patients [post]
func (h *ContentHandler) MigratePatients(c *gin.Context) {
	programID := c.Param("programId")
	if _, err := uuid.Parse(programID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_program_id"})
		return
	}

	identity, ok := auth.IdentityFrom(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if !h.migrationLimiter.allow(identity.AccountID, 10, time.Minute, time.Now()) {
		if h.Metrics != nil {
			h.Metrics.RateLimitedRequests.WithLabelValues("migrate_patients").Inc()
		}
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate_limit_exceeded"})
		return
	}

	var body migrateRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body"})
		return
	}
	if body.PatientSelection == "specific" {
		for _, id := range body.AccountIDs {
			if _, err := uuid.Parse(id); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_account_id"})
				return
			}
		}
	}

	jobID, affected, err := h.Commands.MigratePatients(c.Request.Context(), programID, body.TargetVersion, body.PatientSelection, body.AccountIDs)
	if err != nil {
		switch {
		case errors.Is(err, out.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "target_version_not_found"})
		case errors.Is(err, out.ErrBC4Unavailable):
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "bc4_unavailable"})
		case errors.Is(err, usecase.ErrInvalidInput):
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		default:
			h.logError(c, "migrate patients failed", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		}
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"migration_job_id":        jobID,
		"affected_patients_count": affected,
	})
	if h.Metrics != nil {
		h.Metrics.MigrationTotal.WithLabelValues("requested").Inc()
	}
}
