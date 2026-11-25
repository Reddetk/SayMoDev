// goca:manual — read-контракты BC#3 (endpoints.md#bc3); goca CRUD не совпадает.
package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/port/out"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/usecase"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ContentHandler — публичное чтение каталога и урок BC#3.
type ContentHandler struct {
	Query *usecase.ProgramQuery
}

func (h *ContentHandler) List(c *gin.Context) {
	query := c.Request.URL.Query()
	difficulty := query.Get("difficulty")
	aphasiaType := query.Get("aphasia_type")
	if query.Has("aphasia_type") && aphasiaType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_aphasia_type"})
		return
	}

	items, err := h.Query.List(c.Request.Context(), out.ProgramFilter{
		Difficulty:  difficulty,
		AphasiaType: aphasiaType,
	})
	if err != nil {
		if errors.Is(err, usecase.ErrInvalidFilter) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_filter"})
			return
		}
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
