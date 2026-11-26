package http

import (
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/port/out"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/usecase"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const maxPDFBytes = 100 << 20

type programForm struct {
	Title      string `json:"title"`
	Difficulty string `json:"difficulty"`
	Aphasia    string `json:"aphasiaType"`
	Classifier struct {
		Difficulty  string `json:"difficulty"`
		AphasiaType string `json:"aphasiaType"`
	} `json:"classifier"`
}

func (p programForm) diff() string {
	if p.Classifier.Difficulty != "" {
		return p.Classifier.Difficulty
	}
	return p.Difficulty
}

func (p programForm) aphasia() string {
	if p.Classifier.AphasiaType != "" {
		return p.Classifier.AphasiaType
	}
	return p.Aphasia
}

type lessonForm struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

func readPDF(c *gin.Context, field string) ([]byte, error) {
	header, err := c.FormFile(field)
	if err != nil {
		return nil, nil
	}
	return readMultipartPDF(header)
}

func readMultipartPDF(header *multipart.FileHeader) ([]byte, error) {
	if header.Size > maxPDFBytes {
		return nil, usecase.ErrInvalidInput
	}
	file, err := header.Open()
	if err != nil {
		return nil, err
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, maxPDFBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxPDFBytes {
		return nil, usecase.ErrInvalidInput
	}
	return raw, nil
}

func (h *ContentHandler) writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, out.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
	case errors.Is(err, out.ErrConflict):
		code := "conflict"
		message := err.Error()
		switch {
		case strings.Contains(message, "active_patients"):
			code = "active_patients"
		case strings.Contains(message, "classifier"):
			code = "classifier_exists"
		case strings.Contains(message, "lessons"):
			code = "lessons"
		}
		c.JSON(http.StatusConflict, gin.H{"error": code})
	case errors.Is(err, out.ErrInvalidPosition):
		c.JSON(http.StatusBadRequest, gin.H{"error": "insert_after_position"})
	case errors.Is(err, out.ErrUnavailable):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "s3_unavailable"})
	case errors.Is(err, out.ErrBC4Unavailable):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "bc4_unavailable"})
	case errors.Is(err, usecase.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		h.logError(c, "write operation failed", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
	}
}

// CreateProgram godoc
// @Summary      Create program with lessons
// @Tags         content
// @Security     BearerAuth
// @Accept       multipart/form-data
// @Produce      json
// @Param        program formData string true "JSON {title, classifier}"
// @Param        lessons formData string true "JSON array [{title, description}]"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      401 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Failure      409 {object} map[string]interface{}
// @Failure      503 {object} map[string]interface{}
// @Router       /content/programs [post]
func (h *ContentHandler) CreateProgram(c *gin.Context) {
	var program programForm
	if err := json.Unmarshal([]byte(c.PostForm("program")), &program); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_program"})
		return
	}

	var lessons []lessonForm
	if err := json.Unmarshal([]byte(c.PostForm("lessons")), &lessons); err != nil || len(lessons) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_lessons"})
		return
	}

	uploads := make([]usecase.LessonUpload, 0, len(lessons))
	for i, lesson := range lessons {
		pdf, err := readPDF(c, "lesson_"+strconv.Itoa(i))
		if err != nil {
			h.writeError(c, err)
			return
		}
		uploads = append(uploads, usecase.LessonUpload{Title: lesson.Title, Description: lesson.Description, PDF: pdf})
	}

	record, err := h.Commands.Create(c.Request.Context(), program.Title, program.diff(), program.aphasia(), uploads)
	if err != nil {
		h.writeError(c, err)
		return
	}

	lessonsOut := make([]gin.H, 0, len(record.Lessons))
	for _, lesson := range record.Lessons {
		lessonsOut = append(lessonsOut, gin.H{
			"lessonId": lesson.LessonID, "orderPosition": lesson.OrderPosition,
			"contentUrl": lesson.ContentURL, "createdAt": lesson.CreatedAt, "updatedAt": lesson.UpdatedAt,
		})
	}
	c.JSON(http.StatusCreated, gin.H{
		"programId": record.ProgramID, "version": record.Version,
		"createdAt": record.CreatedAt, "updatedAt": record.UpdatedAt, "lessons": lessonsOut,
	})
}

// UpdateProgram godoc
// @Summary      Update program metadata
// @Tags         content
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        programId path string true "program UUID"
// @Success      200 {object} map[string]interface{}
// @Router       /content/programs/{programId} [patch]
func (h *ContentHandler) UpdateProgram(c *gin.Context) {
	programID := c.Param("programId")
	if _, err := uuid.Parse(programID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_program_id"})
		return
	}

	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body"})
		return
	}
	for key := range body {
		if strings.Contains(strings.ToLower(key), "classifier") || key == "difficulty" || key == "aphasiaType" || key == "aphasia_type" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "classifier_immutable"})
			return
		}
	}
	title, _ := body["title"].(string)
	if err := h.Commands.Rename(c.Request.Context(), programID, title); err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"programId": programID, "title": title})
}

// DeleteProgram godoc
// @Summary      Delete program
// @Tags         content
// @Security     BearerAuth
// @Param        programId path string true "program UUID"
// @Success      204
// @Router       /content/programs/{programId} [delete]
func (h *ContentHandler) DeleteProgram(c *gin.Context) {
	programID := c.Param("programId")
	if _, err := uuid.Parse(programID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_program_id"})
		return
	}
	if err := h.Commands.DeleteProgram(c.Request.Context(), programID); err != nil {
		h.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// AddLesson godoc
// @Summary      Add or insert lesson
// @Tags         content
// @Security     BearerAuth
// @Accept       multipart/form-data
// @Param        programId path string true "program UUID"
// @Success      201 {object} map[string]interface{}
// @Router       /content/programs/{programId}/lessons [post]
func (h *ContentHandler) AddLesson(c *gin.Context) {
	programID := c.Param("programId")
	if _, err := uuid.Parse(programID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_program_id"})
		return
	}

	var lesson lessonForm
	if err := json.Unmarshal([]byte(c.PostForm("lesson")), &lesson); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_lesson"})
		return
	}
	pdf, err := readPDF(c, "pdf_file")
	if err != nil {
		h.writeError(c, err)
		return
	}

	var insertAfter *int
	if raw := c.PostForm("insert_after_position"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_insert_after_position"})
			return
		}
		insertAfter = &value
	}

	record, version, err := h.Commands.AddLesson(c.Request.Context(), programID,
		usecase.LessonUpload{Title: lesson.Title, Description: lesson.Description, PDF: pdf}, insertAfter)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"lessonId": record.LessonID, "orderPosition": record.OrderPosition,
		"contentUrl": record.ContentURL, "createdAt": record.CreatedAt,
		"updatedAt": record.UpdatedAt, "version": version,
	})
}

// UpdateLesson godoc
// @Summary      Update lesson metadata or PDF
// @Tags         content
// @Security     BearerAuth
// @Accept       multipart/form-data
// @Router       /content/programs/{programId}/lessons/{lessonId} [patch]
func (h *ContentHandler) UpdateLesson(c *gin.Context) {
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

	var patch out.LessonPatch
	if value, ok := c.GetPostForm("title"); ok {
		patch.Title = &value
	}
	if value, ok := c.GetPostForm("description"); ok {
		patch.Description = &value
	}
	pdf, err := readPDF(c, "pdf_file")
	if err != nil {
		h.writeError(c, err)
		return
	}

	record, err := h.Commands.UpdateLesson(c.Request.Context(), programID, lessonID, patch, pdf)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"lessonId": record.LessonID, "contentUrl": record.ContentURL})
}

// DeleteLesson godoc
// @Summary      Delete lesson
// @Tags         content
// @Security     BearerAuth
// @Router       /content/programs/{programId}/lessons/{lessonId} [delete]
func (h *ContentHandler) DeleteLesson(c *gin.Context) {
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
	if err := h.Commands.DeleteLesson(c.Request.Context(), programID, lessonID); err != nil {
		h.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
