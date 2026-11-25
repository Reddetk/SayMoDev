package http

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/auth"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/platform/cache"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/port/out"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/usecase"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type seedLesson struct {
	LessonID      string `json:"lessonId"`
	OrderPosition int    `json:"orderPosition"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	ContentURL    string `json:"contentUrl"`
	ContentHash   string `json:"contentHash"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type seedProgram struct {
	ProgramID  string `json:"programId"`
	Title      string `json:"title"`
	Classifier struct {
		Difficulty  string `json:"difficulty"`
		AphasiaType string `json:"aphasiaType"`
	} `json:"classifier"`
	Version   int          `json:"version"`
	CreatedAt time.Time    `json:"createdAt"`
	UpdatedAt time.Time    `json:"updatedAt"`
	Lessons   []seedLesson `json:"lessons"`
}

type fakeProgramRepo struct {
	programs []seedProgram
}

func (r *fakeProgramRepo) latest() map[string]seedProgram {
	latest := map[string]seedProgram{}
	for _, p := range r.programs {
		if current, ok := latest[p.ProgramID]; !ok || p.Version > current.Version {
			latest[p.ProgramID] = p
		}
	}
	return latest
}

func (r *fakeProgramRepo) List(_ context.Context, filter out.ProgramFilter) ([]out.ProgramSummary, error) {
	result := make([]out.ProgramSummary, 0)
	for _, p := range r.latest() {
		if filter.Difficulty != "" && p.Classifier.Difficulty != filter.Difficulty {
			continue
		}
		if filter.AphasiaType != "" && p.Classifier.AphasiaType != filter.AphasiaType {
			continue
		}
		result = append(result, out.ProgramSummary{
			ProgramID:    p.ProgramID,
			Title:        p.Title,
			Difficulty:   p.Classifier.Difficulty,
			AphasiaType:  p.Classifier.AphasiaType,
			Version:      p.Version,
			TotalLessons: len(p.Lessons),
			CreatedAt:    p.CreatedAt,
			UpdatedAt:    p.UpdatedAt,
		})
	}
	return result, nil
}

func (r *fakeProgramRepo) Get(_ context.Context, programID string, version int) (out.ProgramRecord, error) {
	var chosen *seedProgram
	for i := range r.programs {
		p := r.programs[i]
		if p.ProgramID != programID {
			continue
		}
		if version == 0 || p.Version == version {
			if chosen == nil || p.Version > chosen.Version {
				chosen = &r.programs[i]
			}
		}
	}
	if chosen == nil {
		return out.ProgramRecord{}, out.ErrNotFound
	}
	lessons := make([]out.LessonRecord, 0, len(chosen.Lessons))
	for _, l := range chosen.Lessons {
		lessons = append(lessons, out.LessonRecord{
			LessonID:      l.LessonID,
			OrderPosition: l.OrderPosition,
			Title:         l.Title,
			Description:   l.Description,
			ContentURL:    l.ContentURL,
			ContentHash:   l.ContentHash,
			CreatedAt:     l.CreatedAt,
			UpdatedAt:     l.UpdatedAt,
		})
	}
	return out.ProgramRecord{
		ProgramID:   chosen.ProgramID,
		Title:       chosen.Title,
		Difficulty:  chosen.Classifier.Difficulty,
		AphasiaType: chosen.Classifier.AphasiaType,
		Version:     chosen.Version,
		CreatedAt:   chosen.CreatedAt,
		UpdatedAt:   chosen.UpdatedAt,
		Lessons:     lessons,
	}, nil
}

func (r *fakeProgramRepo) GetLesson(_ context.Context, programID, lessonID string) (out.LessonRecord, error) {
	for _, p := range r.programs {
		if p.ProgramID != programID {
			continue
		}
		for _, l := range p.Lessons {
			if l.LessonID == lessonID {
				return out.LessonRecord{
					LessonID:      l.LessonID,
					OrderPosition: l.OrderPosition,
					Title:         l.Title,
					Description:   l.Description,
					ContentURL:    l.ContentURL,
					ContentHash:   l.ContentHash,
					CreatedAt:     l.CreatedAt,
					UpdatedAt:     l.UpdatedAt,
				}, nil
			}
		}
	}
	return out.LessonRecord{}, out.ErrNotFound
}

type fakePresigner struct{}

func (fakePresigner) PresignGet(_ context.Context, contentURL string, _ time.Duration) (string, error) {
	return contentURL + "?X-Amz-Signature=test-signature&X-Amz-Expires=3600", nil
}

type allowStore struct{}

func (allowStore) JTIBlacklisted(context.Context, string) (bool, error) { return false, nil }
func (allowStore) AccountRev(context.Context, string) (int, bool, error) {
	return 0, true, nil
}

func readReadAPIFixtures(t *testing.T) []map[string]string {
	t.Helper()
	path := filepath.Join("..", "..", "..", "testdata", "fixtures", "read_api.csv")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open read_api.csv: %v", err)
	}
	defer f.Close()

	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("read read_api.csv: %v", err)
	}
	header := map[string]int{}
	for i, h := range rows[0] {
		header[h] = i
	}
	out := make([]map[string]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		rec := map[string]string{}
		for k, i := range header {
			rec[k] = row[i]
		}
		out = append(out, rec)
	}
	return out
}

func newContentTestRouter(t *testing.T, programs []seedProgram) (http.Handler, *rsa.PrivateKey, string) {
	t.Helper()

	repo := &fakeProgramRepo{programs: programs}
	query := usecase.NewProgramQuery(repo, fakePresigner{}, nil, nil)
	content := &ContentHandler{Query: query}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		e := big.NewInt(int64(key.PublicKey.E)).Bytes()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "test-kid",
				"n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(e),
			}},
		})
	}))
	t.Cleanup(jwks.Close)

	authn := auth.NewAuthenticator(auth.Options{JWKSURL: jwks.URL}, allowStore{})

	router := NewRouter(Deps{
		Content:     content,
		Auth:        authn,
		CORSOrigins: []string{"http://localhost:3000"},
		Build:       BuildInfo{Version: "test"},
	})
	return router, key, "test-kid"
}

func patientToken(t *testing.T, key *rsa.PrivateKey, kid string) string {
	t.Helper()
	now := time.Now()
	claims := auth.Claims{
		Role:      "patient",
		SessionID: "5c8e1f37-2a4b-4d96-8e03-7f1a6b2c9d85",
		Rev:       1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    auth.Issuer,
			Subject:   "3f2b9c4e-71a5-4d2e-9b8f-0c6a1d5e7f21",
			Audience:  jwt.ClaimStrings{auth.Audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

// TestReadAPIFixtures — таблица testdata/fixtures/read_api.csv.
func TestReadAPIFixtures(t *testing.T) {
	for _, row := range readReadAPIFixtures(t) {
		row := row
		t.Run(row["case_id"], func(t *testing.T) {
			var programs []seedProgram
			if err := json.Unmarshal([]byte(row["programs_json"]), &programs); err != nil {
				t.Fatalf("programs_json: %v", err)
			}

			router, key, kid := newContentTestRouter(t, programs)
			req := httptest.NewRequest(http.MethodGet, row["path"], nil)
			if role := strings.TrimSpace(row["auth"]); role == "patient" || role == "administrator" {
				req.Header.Set("Authorization", "Bearer "+patientToken(t, key, kid))
			}

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			want, err := strconv.Atoi(row["expected_status"])
			if err != nil {
				t.Fatalf("expected_status: %v", err)
			}
			if rec.Code != want {
				t.Fatalf("%s: want %d, got %d (%s)", row["path"], want, rec.Code, rec.Body.String())
			}

			body := rec.Body.String()
			for _, needle := range splitNeedles(row["expected_body_contains"]) {
				if !strings.Contains(body, needle) {
					t.Fatalf("body must contain %q: %s", needle, body)
				}
			}
			for _, needle := range splitNeedles(row["expected_body_not_contains"]) {
				if strings.Contains(body, needle) {
					t.Fatalf("body must not contain %q: %s", needle, body)
				}
			}
		})
	}
}

func splitNeedles(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

var _ = cache.New
var _ = errors.Is
var _ = io.Discard
