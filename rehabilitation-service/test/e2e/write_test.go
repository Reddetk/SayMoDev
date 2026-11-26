//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func tokenForRole(t *testing.T, role string) string {
	t.Helper()
	keyPath := os.Getenv("E2E_SIGN_KEY")
	if keyPath == "" {
		t.Skip("E2E_SIGN_KEY is not set")
	}
	key := loadPrivateKey(t, keyPath)
	kid := os.Getenv("E2E_KID")
	if kid == "" {
		kid = "key-v1"
	}
	sub := os.Getenv("E2E_SUB")
	if sub == "" {
		sub = "3f2b9c4e-71a5-4d2e-9b8f-0c6a1d5e7f21"
	}

	now := time.Now()
	claims := auth.Claims{
		Role:      role,
		SessionID: uuid.NewString(),
		Rev:       1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    auth.Issuer,
			Subject:   sub,
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

func doJSON(t *testing.T, method, url, bearer string, body any) (*http.Response, string) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, string(raw)
}

func doMultipart(t *testing.T, url, bearer string, fields map[string]string, files map[string][]byte) (*http.Response, string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatalf("field %s: %v", name, err)
		}
	}
	for name, content := range files {
		part, err := writer.CreateFormFile(name, name+".pdf")
		if err != nil {
			t.Fatalf("file %s: %v", name, err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, &buffer)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("multipart %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, string(raw)
}

const testPDF = "%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n"

func TestWriteAPIMultipart(t *testing.T) {
	admin := tokenForRole(t, "administrator")
	patient := tokenForRole(t, "patient")

	classifier := "e2e_" + uuid.NewString()[:8]
	createBody := map[string]string{
		"program": fmt.Sprintf(`{"title":"E2E multipart","classifier":{"difficulty":"easy","aphasiaType":%q}}`, classifier),
		"lessons": `[{"title":"Первый урок","description":"e2e"},{"title":"Второй урок"}]`,
	}
	resp, body := doMultipart(t, baseURL()+"/content/programs", admin, createBody, map[string][]byte{
		"lesson_0": []byte(testPDF),
		"lesson_1": []byte(testPDF),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: want 201, got %d (%s)", resp.StatusCode, body)
	}
	var created struct {
		ProgramID string `json:"programId"`
		Version   int    `json:"version"`
		Lessons   []struct {
			LessonID   string `json:"lessonId"`
			ContentURL string `json:"contentUrl"`
		} `json:"lessons"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("create body: %v (%s)", err, body)
	}
	if created.ProgramID == "" || len(created.Lessons) != 2 {
		t.Fatalf("create payload: %+v", created)
	}

	resp, body = doGet(t, baseURL()+"/content/programs/"+created.ProgramID, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, created.ProgramID) {
		t.Fatalf("get program: %d (%s)", resp.StatusCode, body)
	}

	// Presigned URL + реальное скачивание PDF из s3mock.
	resp, body = doGet(t, baseURL()+"/content/programs/"+created.ProgramID+"/lessons/"+created.Lessons[0].LessonID, patient)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get lesson: want 200, got %d (%s)", resp.StatusCode, body)
	}
	var lesson struct {
		ContentURL string `json:"contentUrl"`
	}
	if err := json.Unmarshal([]byte(body), &lesson); err != nil || lesson.ContentURL == "" {
		t.Fatalf("lesson body: %v (%s)", err, body)
	}
	pdfResp, err := (&http.Client{Timeout: 10 * time.Second}).Get(lesson.ContentURL)
	if err != nil {
		t.Fatalf("download presigned: %v", err)
	}
	defer pdfResp.Body.Close()
	pdf, _ := io.ReadAll(pdfResp.Body)
	if pdfResp.StatusCode != http.StatusOK || !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatalf("download: status=%d body=%q", pdfResp.StatusCode, string(pdf[:min(len(pdf), 32)]))
	}

	// append не меняет версию.
	resp, body = doMultipart(t, baseURL()+"/content/programs/"+created.ProgramID+"/lessons", admin,
		map[string]string{"lesson": `{"title":"Третий урок"}`}, map[string][]byte{"pdf_file": []byte(testPDF)})
	if resp.StatusCode != http.StatusCreated || !strings.Contains(body, `"version":1`) {
		t.Fatalf("append: %d (%s)", resp.StatusCode, body)
	}

	// insert после 1 повышает версию.
	resp, body = doMultipart(t, baseURL()+"/content/programs/"+created.ProgramID+"/lessons", admin,
		map[string]string{"lesson": `{"title":"Вставка"}`, "insert_after_position": "1"},
		map[string][]byte{"pdf_file": []byte(testPDF)})
	if resp.StatusCode != http.StatusCreated || !strings.Contains(body, `"version":2`) {
		t.Fatalf("insert: %d (%s)", resp.StatusCode, body)
	}

	// RBAC: patient не может писать.
	resp, body = doMultipart(t, baseURL()+"/content/programs", patient,
		map[string]string{"program": `{"title":"x","classifier":{"difficulty":"easy","aphasiaType":"motor"}}`,
			"lessons": `[{"title":"x"}]`}, map[string][]byte{"lesson_0": []byte(testPDF)})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("patient create: want 403, got %d (%s)", resp.StatusCode, body)
	}

	// rename + classifier immutable.
	resp, body = doJSON(t, http.MethodPatch, baseURL()+"/content/programs/"+created.ProgramID, admin, map[string]any{"title": "E2E renamed"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rename: %d (%s)", resp.StatusCode, body)
	}
	resp, body = doJSON(t, http.MethodPatch, baseURL()+"/content/programs/"+created.ProgramID, admin,
		map[string]any{"classifier": map[string]string{"difficulty": "hard"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("classifier immutable: want 400, got %d (%s)", resp.StatusCode, body)
	}

	// delete lesson + program.
	resp, body = doJSON(t, http.MethodDelete, baseURL()+"/content/programs/"+created.ProgramID+"/lessons/"+created.Lessons[1].LessonID, admin, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete lesson: %d (%s)", resp.StatusCode, body)
	}
	resp, body = doJSON(t, http.MethodDelete, baseURL()+"/content/programs/"+created.ProgramID, admin, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete program: %d (%s)", resp.StatusCode, body)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
