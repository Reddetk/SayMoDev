package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Reddetk/SayMoDev/progress-service/internal/adapter/telemetry"
	corerr "github.com/Reddetk/SayMoDev/progress-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/progress-service/internal/core/valobj"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/in"
)

// --- GET /progress/me ---

// GetMyProgress returns the current program, progress and accessible lesson.
//
//	@Summary	Patient progress
//	@Tags		progress
//	@Security	BearerAuth
//	@Success	200	{object}	in.ProgressView
//	@Failure	401	{object}	map[string]string
//	@Failure	404	{object}	map[string]string
//	@Router		/progress/me [get]
func (h *Handlers) GetMyProgress(c *gin.Context) {
	accountID, ok := AccountID(c)
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"})
		return
	}
	view, err := h.progress.GetMyProgress(c.Request.Context(), accountID)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// --- POST /progress/me/activate ---

type activateRequest struct {
	ProgramID  string `json:"programId"`
	Classifier *struct {
		Difficulty  string `json:"difficulty"`
		AphasiaType string `json:"aphasiaType"`
	} `json:"classifier"`
}

// ActivateProgram activates the initial program manually (Kafka fallback).
//
//	@Summary	Activate program
//	@Tags		progress
//	@Security	BearerAuth
//	@Param		Idempotency-Key	header	string	true	"UUID v4"
//	@Success	201				{object}	in.ActivateResult
//	@Failure	409				{object}	map[string]string
//	@Failure	503				{object}	map[string]string
//	@Router		/progress/me/activate [post]
func (h *Handlers) ActivateProgram(c *gin.Context) {
	accountID, ok := AccountID(c)
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"})
		return
	}
	var req activateRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, http.ErrBodyNotAllowed) {
		badRequest(c, "invalid_request_body")
		return
	}
	cmd := in.ActivateCommand{AccountID: accountID}
	if req.ProgramID != "" {
		id, err := uuid.Parse(req.ProgramID)
		if err != nil {
			badRequest(c, "invalid_path_param")
			return
		}
		cmd.ProgramID = &id
	}
	if req.Classifier != nil {
		cmd.HasClassifier = true
		cmd.Difficulty = req.Classifier.Difficulty
		cmd.AphasiaType = req.Classifier.AphasiaType
	}
	if cmd.ProgramID == nil && !cmd.HasClassifier {
		fail(c, corerr.ErrNoProgramForClassifier)
		return
	}
	res, err := h.programs.Activate(c.Request.Context(), cmd)
	if err != nil {
		fail(c, err)
		return
	}
	telemetry.ProgramsActivatedTotal.Inc()
	c.JSON(http.StatusCreated, res)
}

// --- DELETE /progress/me ---

// DeleteMyProgress anonymises answers and deletes progress (GDPR).
//
//	@Summary	Self-delete progress
//	@Tags		progress
//	@Security	BearerAuth
//	@Param		X-Confirmation	header	string	true	"delete-my-progress"
//	@Success	204
//	@Failure	400	{object}	map[string]string
//	@Router		/progress/me [delete]
func (h *Handlers) DeleteMyProgress(c *gin.Context) {
	accountID, ok := AccountID(c)
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"})
		return
	}
	if err := h.deleter.DeleteMyProgress(c.Request.Context(), in.DeleteCommand{
		AccountID: accountID,
		Confirm:   c.GetHeader("X-Confirmation"),
	}); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- answers ---

type presignRequest struct {
	MediaType   string `json:"mediaType"`
	ContentType string `json:"contentType"`
}

// PresignAnswer returns a presigned PUT URL for a photo/video answer.
//
//	@Summary	Presign answer upload
//	@Tags		answers
//	@Security	BearerAuth
//	@Success	201	{object}	in.PresignResult
//	@Failure	403	{object}	map[string]string
//	@Failure	409	{object}	map[string]string
//	@Router		/progress/lessons/{lessonId}/answers/presign [post]
func (h *Handlers) PresignAnswer(c *gin.Context) {
	accountID, lessonID, ok := h.patientAndLesson(c)
	if !ok {
		return
	}
	var req presignRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid_payload"})
		return
	}
	res, err := h.answers.Presign(c.Request.Context(), in.PresignCommand{
		AccountID: accountID, LessonID: lessonID, MediaType: req.MediaType, ContentType: req.ContentType,
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, res)
}

type answerRequest struct {
	MediaType string `json:"mediaType"`
	Payload   string `json:"payload"`
}

// SubmitAnswer appends a lesson answer (Model B phase 1).
//
//	@Summary	Submit answer
//	@Tags		answers
//	@Security	BearerAuth
//	@Param		Idempotency-Key	header	string	true	"UUID v4"
//	@Success	201				{object}	in.SubmitAnswerResult
//	@Failure	403				{object}	map[string]string
//	@Failure	409				{object}	map[string]string
//	@Failure	422				{object}	map[string]string
//	@Router		/progress/lessons/{lessonId}/answers [post]
func (h *Handlers) SubmitAnswer(c *gin.Context) {
	accountID, lessonID, ok := h.patientAndLesson(c)
	if !ok {
		return
	}
	var req answerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid_payload"})
		return
	}
	res, err := h.answers.Submit(c.Request.Context(), in.SubmitAnswerCommand{
		AccountID: accountID, LessonID: lessonID, MediaType: req.MediaType, Payload: req.Payload,
	})
	if err != nil {
		fail(c, err)
		return
	}
	telemetry.LessonsAnswersSubmittedTotal.WithLabelValues(req.MediaType).Inc()
	c.JSON(http.StatusCreated, res)
}

// --- complete ---

// CompleteLesson finalises a lesson (Model B phase 2, irreversible).
//
//	@Summary	Complete lesson
//	@Tags		progress
//	@Security	BearerAuth
//	@Param		Idempotency-Key	header	string	true	"UUID v4"
//	@Success	200				{object}	in.CompleteResult
//	@Failure	403				{object}	map[string]string
//	@Failure	409				{object}	map[string]string
//	@Failure	422				{object}	map[string]string
//	@Router		/progress/lessons/{lessonId}/complete [post]
func (h *Handlers) CompleteLesson(c *gin.Context) {
	accountID, lessonID, ok := h.patientAndLesson(c)
	if !ok {
		return
	}
	res, err := h.completer.Complete(c.Request.Context(), in.CompleteCommand{AccountID: accountID, LessonID: lessonID})
	if err != nil {
		recordCompletionRejection(err)
		fail(c, err)
		return
	}
	telemetry.LessonsCompletedTotal.Inc()
	if res.CompletionPercent >= 100 {
		telemetry.ProgramsCompletedTotal.Inc()
	}
	c.JSON(http.StatusOK, res)
}

// --- difficulty ---

type difficultyRequest struct {
	NewDifficulty string `json:"newDifficulty"`
	AphasiaType   string `json:"aphasiaType"`
}

// ChangeDifficulty switches the patient to a program of another difficulty.
//
//	@Summary	Change difficulty
//	@Tags		progress
//	@Security	BearerAuth
//	@Param		Idempotency-Key	header	string	true	"UUID v4"
//	@Success	200				{object}	in.ChangeDifficultyResult
//	@Failure	400				{object}	map[string]string
//	@Failure	503				{object}	map[string]string
//	@Router		/progress/difficulty [patch]
func (h *Handlers) ChangeDifficulty(c *gin.Context) {
	accountID, ok := AccountID(c)
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"})
		return
	}
	var req difficultyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid_difficulty")
		return
	}
	res, err := h.programs.ChangeDifficulty(c.Request.Context(), in.ChangeDifficultyCommand{
		AccountID: accountID, NewDifficulty: req.NewDifficulty,
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// --- resume ---

// ResumeProgram resumes a paused program.
//
//	@Summary	Resume program
//	@Tags		progress
//	@Security	BearerAuth
//	@Param		Idempotency-Key	header	string	true	"UUID v4"
//	@Success	200				{object}	in.ResumeResult
//	@Failure	400				{object}	map[string]string
//	@Failure	404				{object}	map[string]string
//	@Router		/progress/programs/{programId}/resume [post]
func (h *Handlers) ResumeProgram(c *gin.Context) {
	accountID, ok := AccountID(c)
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"})
		return
	}
	programID, ok := pathUUID(c, "programId")
	if !ok {
		return
	}
	res, err := h.programs.Resume(c.Request.Context(), in.ResumeCommand{AccountID: accountID, ProgramID: programID})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// --- admin ---

// AdminGetProgress returns any patient's progress.
//
//	@Summary	Admin: patient progress
//	@Tags		admin
//	@Security	BearerAuth
//	@Success	200	{object}	in.AdminProgressView
//	@Router		/admin/progress/{accountId} [get]
func (h *Handlers) AdminGetProgress(c *gin.Context) {
	accountID, ok := pathUUID(c, "accountId")
	if !ok {
		return
	}
	view, err := h.admin.GetProgress(c.Request.Context(), accountID)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// AdminListAnswers returns all patient answers with pagination.
//
//	@Summary	Admin: all answers
//	@Tags		admin
//	@Security	BearerAuth
//	@Success	200	{object}	in.AdminAnswersPage
//	@Router		/admin/answers/{accountId} [get]
func (h *Handlers) AdminListAnswers(c *gin.Context) {
	accountID, ok := pathUUID(c, "accountId")
	if !ok {
		return
	}
	q := in.AdminAnswersQuery{AccountID: accountID}
	if raw := c.Query("lessonId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			badRequest(c, "invalid_query")
			return
		}
		q.LessonID = &id
	}
	if mt := c.Query("mediaType"); mt != "" {
		if !valobj.MediaType(mt).Valid() {
			badRequest(c, "invalid_query")
			return
		}
		q.MediaType = mt
	}
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			badRequest(c, "invalid_query")
			return
		}
		q.Limit = n
	}
	if raw := c.Query("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			badRequest(c, "invalid_query")
			return
		}
		q.Offset = n
	}
	page, err := h.admin.ListAnswers(c.Request.Context(), q)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

// AdminLessonAnswers returns answers for one lesson, chronological.
//
//	@Summary	Admin: lesson answers
//	@Tags		admin
//	@Security	BearerAuth
//	@Success	200	{array}	in.AdminAnswerView
//	@Router		/admin/answers/{accountId}/{lessonId} [get]
func (h *Handlers) AdminLessonAnswers(c *gin.Context) {
	accountID, ok := pathUUID(c, "accountId")
	if !ok {
		return
	}
	lessonID, ok := pathUUID(c, "lessonId")
	if !ok {
		return
	}
	answers, err := h.admin.ListLessonAnswers(c.Request.Context(), accountID, lessonID)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, answers)
}

// AdminChangeDifficulty changes a patient's program difficulty.
//
//	@Summary	Admin: change difficulty
//	@Tags		admin
//	@Security	BearerAuth
//	@Success	200	{object}	in.ChangeDifficultyResult
//	@Router		/admin/progress/{accountId}/difficulty [patch]
func (h *Handlers) AdminChangeDifficulty(c *gin.Context) {
	accountID, ok := pathUUID(c, "accountId")
	if !ok {
		return
	}
	var req difficultyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid_difficulty")
		return
	}
	res, err := h.adminOp.ChangeDifficulty(c.Request.Context(), in.ChangeDifficultyCommand{
		AccountID: accountID, NewDifficulty: req.NewDifficulty, AphasiaType: req.AphasiaType,
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// ActivePatients is the synchronous deletion guard consumed by BC#3.
//
//	@Summary	Deletion guard: active patients
//	@Tags		admin
//	@Param		X-Service-Token	header	string	true	"Shared service-to-service secret"
//	@Success	200	{object}	in.ActivePatientsView
//	@Failure	401	{object}	map[string]string
//	@Router		/admin/progress/program/{programId}/version/{version}/active-patients [get]
func (h *Handlers) ActivePatients(c *gin.Context) {
	programID, ok := pathUUID(c, "programId")
	if !ok {
		return
	}
	version, err := strconv.Atoi(c.Param("version"))
	if err != nil || version < 1 {
		badRequest(c, "invalid_path_param")
		return
	}
	view, err := h.guard.ActivePatients(c.Request.Context(), programID, version)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

func (h *Handlers) patientAndLesson(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	accountID, ok := AccountID(c)
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"})
		return uuid.Nil, uuid.Nil, false
	}
	lessonID, ok := pathUUID(c, "lessonId")
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	return accountID, lessonID, true
}

func pathUUID(c *gin.Context, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		badRequest(c, "invalid_path_param")
		return uuid.Nil, false
	}
	return id, true
}

// now is used only for latency measurement helpers.
var _ = time.Now
