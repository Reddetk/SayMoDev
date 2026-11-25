//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

const seedProgramID = "3f2a9c11-7b4d-4e21-9c8a-1d2e3f4a5b6c"

func doGet(t *testing.T, url string, bearer string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()

	var body strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		body.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp, body.String()
}

func TestReadAPISeeded(t *testing.T) {
	resp, body := doGet(t, baseURL()+"/content/programs", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: want 200, got %d (%s)", resp.StatusCode, body)
	}
	if !strings.Contains(body, seedProgramID) {
		t.Fatalf("list must contain seeded program: %s", body)
	}
	if strings.Contains(body, `"lessons"`) || strings.Contains(body, "contentHash") {
		t.Fatalf("list contract must not expose lessons/contentHash: %s", body)
	}

	resp, body = doGet(t, baseURL()+"/content/programs?difficulty=easy", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, seedProgramID) {
		t.Fatalf("filter easy: want 200 with seed, got %d (%s)", resp.StatusCode, body)
	}

	resp, body = doGet(t, baseURL()+"/content/programs?difficulty=superhard", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid filter: want 400, got %d (%s)", resp.StatusCode, body)
	}

	resp, body = doGet(t, baseURL()+"/content/programs/"+seedProgramID, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get program: want 200, got %d (%s)", resp.StatusCode, body)
	}
	var program struct {
		ProgramID string `json:"programId"`
		Version   int    `json:"version"`
		Lessons   []struct {
			LessonID   string `json:"lessonId"`
			ContentURL string `json:"contentUrl"`
		} `json:"lessons"`
	}
	if err := json.Unmarshal([]byte(body), &program); err != nil {
		t.Fatalf("program body: %v (%s)", err, body)
	}
	if program.ProgramID != seedProgramID || program.Version != 1 || len(program.Lessons) == 0 {
		t.Fatalf("unexpected program: %+v", program)
	}

	resp, _ = doGet(t, baseURL()+"/content/programs/"+seedProgramID+"?version=99", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing version: want 404, got %d", resp.StatusCode)
	}

	resp, body = doGet(t, baseURL()+"/content/programs/"+seedProgramID+"/lessons/"+program.Lessons[0].LessonID, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("lesson without JWT: want 401, got %d (%s)", resp.StatusCode, body)
	}

	if token := os.Getenv("E2E_TOKEN"); token != "" {
		resp, body = doGet(t, baseURL()+"/content/programs/"+seedProgramID+"/lessons/"+program.Lessons[0].LessonID, token)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("lesson with JWT: want 200, got %d (%s)", resp.StatusCode, body)
		}
		if !strings.Contains(body, "X-Amz-Signature") && !strings.Contains(body, "X-Amz-Expires") {
			t.Fatalf("lesson contentUrl must be presigned: %s", body)
		}
	}
}
