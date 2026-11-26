package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/Reddetk/SayMoDev/progress-service/internal/config"
	corerr "github.com/Reddetk/SayMoDev/progress-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/progress-service/internal/logger"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

type stubLogger struct{}

func (stubLogger) Debug(string, ...zap.Field) {}
func (stubLogger) Info(string, ...zap.Field)  {}
func (stubLogger) Warn(string, ...zap.Field)  {}
func (stubLogger) Error(string, ...zap.Field) {}
func (stubLogger) Fatal(string, ...zap.Field) {}

type stubValidator struct {
	auth in.AuthContext
	err  error
}

func (s stubValidator) ValidateToken(context.Context, string) (in.AuthContext, error) {
	return s.auth, s.err
}

type stubIdem struct{}

func (stubIdem) Begin(context.Context, string, time.Duration) (out.IdempotencyState, *out.StoredResponse, error) {
	return out.IdempotencyNew, nil, nil
}
func (stubIdem) Complete(context.Context, string, out.StoredResponse, time.Duration) error {
	return nil
}
func (stubIdem) Release(context.Context, string) error { return nil }
func (stubIdem) Ping(context.Context) error            { return nil }

type stubProgress struct {
	view in.ProgressView
	err  error
}

func (s stubProgress) GetMyProgress(context.Context, uuid.UUID) (in.ProgressView, error) {
	return s.view, s.err
}

type stubPrograms struct{ err error }

func (s stubPrograms) Activate(context.Context, in.ActivateCommand) (in.ActivateResult, error) {
	return in.ActivateResult{AccountID: "a", ProgramID: "p", Status: "active"}, s.err
}
func (s stubPrograms) ChangeDifficulty(context.Context, in.ChangeDifficultyCommand) (in.ChangeDifficultyResult, error) {
	return in.ChangeDifficultyResult{}, s.err
}
func (s stubPrograms) Resume(context.Context, in.ResumeCommand) (in.ResumeResult, error) {
	return in.ResumeResult{}, s.err
}

type stubAnswers struct{ err error }

func (s stubAnswers) Presign(context.Context, in.PresignCommand) (in.PresignResult, error) {
	return in.PresignResult{}, s.err
}
func (s stubAnswers) Submit(context.Context, in.SubmitAnswerCommand) (in.SubmitAnswerResult, error) {
	return in.SubmitAnswerResult{}, s.err
}

type stubCompleter struct{ err error }

func (s stubCompleter) Complete(context.Context, in.CompleteCommand) (in.CompleteResult, error) {
	return in.CompleteResult{CompletionPercent: 33}, s.err
}

type stubDeleter struct{ err error }

func (s stubDeleter) DeleteMyProgress(context.Context, in.DeleteCommand) error { return s.err }

type stubAdmin struct{ err error }

func (s stubAdmin) GetProgress(context.Context, uuid.UUID) (in.AdminProgressView, error) {
	return in.AdminProgressView{AccountID: "a"}, s.err
}
func (s stubAdmin) ListAnswers(context.Context, in.AdminAnswersQuery) (in.AdminAnswersPage, error) {
	return in.AdminAnswersPage{}, s.err
}
func (s stubAdmin) ListLessonAnswers(context.Context, uuid.UUID, uuid.UUID) ([]in.AdminAnswerView, error) {
	return nil, s.err
}

type stubAdminOp struct{ err error }

func (s stubAdminOp) ChangeDifficulty(context.Context, in.ChangeDifficultyCommand) (in.ChangeDifficultyResult, error) {
	return in.ChangeDifficultyResult{}, s.err
}

type stubGuard struct{ view in.ActivePatientsView }

func (s stubGuard) ActivePatients(context.Context, uuid.UUID, int) (in.ActivePatientsView, error) {
	return s.view, nil
}

func testRouter(t *testing.T, opts ...func(*Deps)) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	patient := in.AuthContext{AccountID: "11111111-1111-1111-1111-111111111111", Role: "patient", Rev: 1}
	admin := in.AuthContext{AccountID: "22222222-2222-2222-2222-222222222222", Role: "administrator", Rev: 1}
	deps := Deps{
		Config:    &config.Config{Environment: "test", IdempotencyTTL: time.Hour, InternalServiceToken: "svc"},
		Log:       stubLogger{},
		Validator: stubValidator{auth: patient},
		Idem:      stubIdem{},
		Progress:  stubProgress{view: in.ProgressView{AccountID: patient.AccountID, Status: "active"}},
		Programs:  stubPrograms{},
		Answers:   stubAnswers{},
		Completer: stubCompleter{},
		Deleter:   stubDeleter{},
		Admin:     stubAdmin{},
		AdminOp:   stubAdminOp{},
		Guard:     stubGuard{view: in.ActivePatientsView{ProgramID: "p", Version: 1}},
	}
	for _, opt := range opts {
		opt(&deps)
	}
	_ = admin
	return NewRouter(deps)
}

func do(t *testing.T, r http.Handler, method, path, token string, headers map[string]string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAuthRequired(t *testing.T) {
	r := testRouter(t)
	w := do(t, r, http.MethodGet, "/progress/me", "", nil, "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_token")
}

func TestGetProgressOK(t *testing.T) {
	r := testRouter(t)
	w := do(t, r, http.MethodGet, "/progress/me", "token", nil, "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "11111111-1111-1111-1111-111111111111")
}

func TestIdempotencyKeyRequired(t *testing.T) {
	r := testRouter(t)
	w := do(t, r, http.MethodPost, "/progress/me/activate", "token", nil, `{"classifier":{"difficulty":"easy"}}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "idempotency_key_required")
}

func TestAdminForbiddenForPatient(t *testing.T) {
	r := testRouter(t)
	w := do(t, r, http.MethodGet, "/admin/progress/11111111-1111-1111-1111-111111111111", "token", nil, "")
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAdminAllowed(t *testing.T) {
	r := testRouter(t, func(d *Deps) {
		d.Validator = stubValidator{auth: in.AuthContext{AccountID: "22222222-2222-2222-2222-222222222222", Role: "administrator"}}
	})
	w := do(t, r, http.MethodGet, "/admin/progress/11111111-1111-1111-1111-111111111111", "token", nil, "")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCompleteRejectedMapsToConflict(t *testing.T) {
	r := testRouter(t, func(d *Deps) { d.Completer = stubCompleter{err: corerr.ErrLessonAlreadyCompleted} })
	w := do(t, r, http.MethodPost, "/progress/lessons/11111111-1111-1111-1111-111111111111/complete", "token",
		map[string]string{"Idempotency-Key": uuid.NewString()}, "")
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "lesson_already_completed")
}

func TestNoAnswerMapsToUnprocessable(t *testing.T) {
	r := testRouter(t, func(d *Deps) { d.Completer = stubCompleter{err: corerr.ErrNoAnswerSubmitted} })
	w := do(t, r, http.MethodPost, "/progress/lessons/11111111-1111-1111-1111-111111111111/complete", "token",
		map[string]string{"Idempotency-Key": uuid.NewString()}, "")
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "no_answer_submitted")
}

func TestCatalogUnavailableMapsTo503(t *testing.T) {
	r := testRouter(t, func(d *Deps) { d.Programs = stubPrograms{err: corerr.ErrProgramCatalogUnavailable} })
	w := do(t, r, http.MethodPatch, "/progress/difficulty", "token",
		map[string]string{"Idempotency-Key": uuid.NewString()}, `{"newDifficulty":"hard"}`)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestInvalidLessonUUID(t *testing.T) {
	r := testRouter(t)
	w := do(t, r, http.MethodPost, "/progress/lessons/nope/complete", "token",
		map[string]string{"Idempotency-Key": uuid.NewString()}, "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_path_param")
}

func TestDeletionGuardRequiresServiceToken(t *testing.T) {
	r := testRouter(t)
	w := do(t, r, http.MethodGet, "/admin/progress/program/11111111-1111-1111-1111-111111111111/version/1/active-patients", "", nil, "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	w = do(t, r, http.MethodGet, "/admin/progress/program/11111111-1111-1111-1111-111111111111/version/1/active-patients", "",
		map[string]string{"X-Service-Token": "svc"}, "")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAlreadyActiveMapsTo409(t *testing.T) {
	r := testRouter(t, func(d *Deps) { d.Programs = stubPrograms{err: corerr.ErrProgramAlreadyActive} })
	w := do(t, r, http.MethodPost, "/progress/me/activate", "token",
		map[string]string{"Idempotency-Key": uuid.NewString()}, `{"programId":"11111111-1111-1111-1111-111111111111"}`)
	assert.Equal(t, http.StatusConflict, w.Code)
}

var (
	_ logger.Logger = stubLogger{}
)
