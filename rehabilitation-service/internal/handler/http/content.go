// goca:manual — read-контракты BC#3 (endpoints.md#bc3); goca CRUD не совпадает.
package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/platform/metrics"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/port/out"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/usecase"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ContentHandler — публичное чтение каталога и урок BC#3.
type ContentHandler struct {
	Query            *usecase.ProgramQuery
	Commands         *usecase.ProgramCommand
	Metrics          *metrics.Metrics
	Log              *zap.Logger
	migrationLimiter migrationLimiter
}

func (h *ContentHandler) logError(c *gin.Context, message string, err error) {
	if h.Log == nil {
		return
	}
	h.Log.Error(message,
		zap.String("path", c.Request.URL.Path),
		zap.String("request_id", c.GetString("request_id")),
		zap.Error(err),
	)
}

// ListPrograms godoc
// @Summary      List rehabilitation programs
// @Description  Public catalog: latest version of each program. Filters are optional.
// @Tags         content
// @Produce      json
// @Param        difficulty    query string false "easy|medium|hard"
// @Param        aphasia_type  query string false "aphasia type, up to 50 chars"
// @Success      200 {array}  map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Router       /content/programs [get]
func (h *ContentHandler) List(c *gin.Context) {
	query := c.Request.URL.Query()
	items, err := h.Query.List(c.Request.Context(), out.ProgramFilter{
		Difficulty:  query.Get("difficulty"),
		AphasiaType: query.Get("aphasia_type"),
	})
	if err != nil {
		if errors.Is(err, usecase.ErrInvalidFilter) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_filter"})
			return
		}
		h.logError(c, "list programs failed", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}

	response := make([]gin.H, 0, len(items))
	for _, item := range items {
		response = append(response, gin.H{
			"programId":     item.ProgramID,
			"title":         item.Title,
			"classifier":    gin.H{"difficulty": item.Difficulty, "aphasiaType": item.AphasiaType},
			"version":       item.Version,
			"total_lessons": item.TotalLessons,
			"createdAt":     item.CreatedAt,
			"updatedAt":     item.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, response)
}

// GetProgram godoc
// @Summary      Get program by id and version
// @Description  Public program card. version defaults to the latest; lessons belong to that version.
// @Tags         content
// @Produce      json
// @Param        programId path  string true  "program UUID"
// @Param        version   query int    false "program version"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /content/programs/{programId} [get]
func (h *ContentHandler) Get(c *gin.Context) {
	programID := c.Param("programId")
	if _, err := uuid.Parse(programID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_program_id"})
		return
	}

	version := 0
	if raw := c.Query("version"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_version"})
			return
		}
		version = parsed
	}

	record, err := h.Query.Get(c.Request.Context(), programID, version)
	if err != nil {
		if errors.Is(err, out.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "program_not_found"})
			return
		}
		h.logError(c, "get program failed", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}

	lessons := make([]gin.H, 0, len(record.Lessons))
	for _, lesson := range record.Lessons {
		lessons = append(lessons, gin.H{
			"lessonId":      lesson.LessonID,
			"orderPosition": lesson.OrderPosition,
			"title":         lesson.Title,
			"contentUrl":    lesson.ContentURL,
			"contentHash":   lesson.ContentHash,
			"createdAt":     lesson.CreatedAt,
			"updatedAt":     lesson.UpdatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"programId":  record.ProgramID,
		"title":      record.Title,
		"classifier": gin.H{"difficulty": record.Difficulty, "aphasiaType": record.AphasiaType},
		"version":    record.Version,
		"createdAt":  record.CreatedAt,
		"updatedAt":  record.UpdatedAt,
		"lessons":    lessons,
	})
}

// GetLesson godoc
// @Summary      Get lesson with presigned PDF URL
// @Description  Requires a valid patient/administrator JWT. contentUrl is a presigned GET URL (TTL 1h).
// @Tags         content
// @Produce      json
// @Security     BearerAuth
// @Param        programId path string true "program UUID"
// @Param        lessonId  path string true "lesson UUID"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      401 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /content/programs/{programId}/lessons/{lessonId} [get]
func (h *ContentHandler) GetLesson(c *gin.Context) {
	programID := c.Param("programId")
	lessonID := c.Param("lessonId")
	if _, err := uuid.Parse(programID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_program_id"})
		return
	}
	if _, err := uuid.Parse(lessonID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_lesson_id"})
		return
	}

	lesson, err := h.Query.GetLesson(c.Request.Context(), programID, lessonID)
	if err != nil {
		if errors.Is(err, out.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "lesson_not_found"})
			return
		}
		h.logError(c, "get lesson failed", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"lessonId":      lesson.LessonID,
		"title":         lesson.Title,
		"description":   lesson.Description,
		"contentUrl":    lesson.ContentURL,
		"orderPosition": lesson.OrderPosition,
		"contentHash":   lesson.ContentHash,
	})
}
