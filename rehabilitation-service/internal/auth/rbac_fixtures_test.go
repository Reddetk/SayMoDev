package auth

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TestRBACFixtures — таблица testdata/fixtures/rbac.csv.
func TestRBACFixtures(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)

	key := newRSAKey(t)
	srv := jwksServer(t, &key.PublicKey)
	defer srv.Close()

	now := time.Now().UTC()
	auth := NewAuthenticator(Options{
		JWKSURL: srv.URL,
		Now:     func() time.Time { return now },
	}, &fakeStore{accountRev: intPtr(0)})

	ok := func(c *gin.Context) { c.Status(http.StatusOK) }

	router := gin.New()
	// Публичное чтение — без JWT (endpoints.md#bc3).
	router.GET("/content/programs", ok)
	router.GET("/content/programs/:programId", ok)

	// Write — аутентификация + administrator.
	admin := router.Group("", auth.Middleware(), auth.RequireAdmin())
	admin.POST("/content/programs", ok)
	admin.PATCH("/content/programs/:programId", ok)
	admin.DELETE("/content/programs/:programId", ok)
	admin.POST("/content/programs/:programId/lessons", ok)
	admin.PATCH("/content/programs/:programId/lessons/:lessonId", ok)
	admin.DELETE("/content/programs/:programId/lessons/:lessonId", ok)
	admin.POST("/admin/content/programs/:programId/migrate-patients", ok)

	const (
		programID = "11111111-1111-4111-8111-111111111111"
		lessonID  = "22222222-2222-4222-8222-222222222222"
	)

	for _, row := range readAuthCSV(t, "rbac.csv") {
		row := row
		t.Run(row["case_id"], func(t *testing.T) {
			method := row["method"]
			path := strings.ReplaceAll(row["path"], "{program_id}", programID)
			path = strings.ReplaceAll(path, "{lesson_id}", lessonID)

			req := httptest.NewRequest(method, path, nil)
			role := strings.TrimSpace(row["role"])
			if role != "" {
				// jti уникален для токена: иначе L1-кэш вернёт identity предыдущего кейса.
				caseJTI := uuid.NewSHA1(uuid.NameSpaceOID, []byte(row["case_id"])).String()
				token := signForFixture(t, key, "valid", "RS256", testKID, Claims{
					Role:      role,
					SessionID: "5c8e1f37-2a4b-4d96-8e03-7f1a6b2c9d85",
					Rev:       1,
					RegisteredClaims: registered(
						"3f2b9c4e-71a5-4d2e-9b8f-0c6a1d5e7f21",
						caseJTI,
						now,
					),
				})
				req.Header.Set("Authorization", "Bearer "+token)
			}

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			want, _ := strconv.Atoi(row["expected_status"])
			if rec.Code != want {
				t.Fatalf("%s %s role=%q: want %d, got %d (%s)", method, path, role, want, rec.Code, rec.Body.String())
			}
		})
	}
}
