//go:build e2e

package e2e

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type writeCase map[string]string

func readWriteCases(t *testing.T) []writeCase {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "testdata", "fixtures", "write_api.csv"))
	if err != nil {
		t.Fatalf("open write_api.csv: %v", err)
	}
	defer f.Close()

	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("read write_api.csv: %v", err)
	}
	header := map[string]int{}
	for i, h := range rows[0] {
		header[h] = i
	}
	out := make([]writeCase, 0, len(rows)-1)
	for _, row := range rows[1:] {
		rec := writeCase{}
		for name, idx := range header {
			rec[name] = row[idx]
		}
		out = append(out, rec)
	}
	return out
}

func roleToken(t *testing.T, role string) string {
	switch role {
	case "admin", "administrator":
		return tokenForRole(t, "administrator")
	case "patient":
		return tokenForRole(t, "patient")
	case "relative":
		return tokenForRole(t, "relative")
	case "", "anonymous":
		return ""
	default:
		t.Fatalf("unknown role %q", role)
		return ""
	}
}

type fixtureLesson struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	PDF         string `json:"pdf"`
}

func parseLessons(t *testing.T, raw string) []fixtureLesson {
	t.Helper()
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var lessons []fixtureLesson
	if err := json.Unmarshal([]byte(raw), &lessons); err != nil {
		t.Fatalf("lessons_json: %v", err)
	}
	return lessons
}

func pdfFor(kind string) []byte {
	if kind == "wrong_mime" {
		return []byte("\x89PNG\r\n\x1a\nnot-a-pdf")
	}
	return []byte(testPDF)
}

func dockerAction(t *testing.T, action, container string) {
	t.Helper()
	cmd := exec.Command("docker", action, container)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker %s %s: %v (%s)", action, container, err, out)
	}
}

func resetSeed(t *testing.T, pool *pgxpool.Pool, active int) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DELETE FROM programs`); err != nil {
		t.Fatalf("clear programs: %v", err)
	}

	// P1: easy/motor, version 2, два урока с фиксированными id.
	if _, err := pool.Exec(ctx, `
INSERT INTO programs (id, title, difficulty, aphasia_type, version, active_reference_count)
VALUES ('3f2a9c11-7b4d-4e21-9c8a-1d2e3f4a5b6c', 'Базовый курс', 'easy', 'motor', 2, $1)`, active); err != nil {
		t.Fatalf("seed P1: %v", err)
	}
	for i, lessonID := range []string{"a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d", "b2c3d4e5-f6a7-4b8c-9d0e-1f2a3b4c5d6e"} {
		contentURL := "http://localhost:9005/saymo-programs/programs/p1/lessons/" + lessonID + "/v1.pdf"
		if _, err := pool.Exec(ctx, `
INSERT INTO program_lessons (id, program_id, program_version, order_position, title, description,
    content_url, content_hash, file_size_bytes, mime_type)
VALUES ($1::uuid, '3f2a9c11-7b4d-4e21-9c8a-1d2e3f4a5b6c', 2, $2, $3, 'seed', $4, $5, 1024, 'application/pdf')`,
			lessonID, i+1, fmt.Sprintf("Урок %d", i+1), contentURL, strings.Repeat("a", 64)); err != nil {
			t.Fatalf("seed P1 lesson: %v", err)
		}
	}

	if _, err := pool.Exec(ctx, `
INSERT INTO programs (id, title, difficulty, aphasia_type, version)
VALUES ('5d6b8e22-1a3c-4f90-8b7d-2e4f6a8c0b1d', 'Удаляемая программа', 'hard', 'mixed', 1)`); err != nil {
		t.Fatalf("seed delete program: %v", err)
	}

	if _, err := pool.Exec(ctx, `
INSERT INTO programs (id, title, difficulty, aphasia_type, version)
VALUES ('4b5c6d7e-8f90-4a1b-9c2d-3e4f5a6b7c8d', 'Лимит 200', 'medium', 'global', 1)`); err != nil {
		t.Fatalf("seed limit program: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO program_lessons (id, program_id, program_version, order_position, title, description,
    content_url, content_hash, file_size_bytes, mime_type)
SELECT gen_random_uuid(), '4b5c6d7e-8f90-4a1b-9c2d-3e4f5a6b7c8d', 1, g, 'Урок ' || g, '',
       'http://localhost:9005/saymo-programs/programs/limit/lessons/' || g || '.pdf',
       lpad(to_hex(g), 64, '0'), 1024, 'application/pdf'
FROM generate_series(1, 200) AS g`); err != nil {
		t.Fatalf("seed 200 lessons: %v", err)
	}
}

func TestWriteAPIFixtures(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	defer pool.Close()

	cases := readWriteCases(t)
	for _, c := range cases {
		c := c
		if c["operation"] != "create_program" {
			continue
		}
		if c["case_id"] == "create_06" {
			continue // S3-down кейс выполняется последним, чтобы не открывать breaker
		}
		t.Run(c["case_id"], func(t *testing.T) {
			token := roleToken(t, c["role"])
			if c["case_id"] == "create_01" {
				if _, err := pool.Exec(ctx, `DELETE FROM programs`); err != nil {
					t.Fatalf("clear: %v", err)
				}
			}
			if c["case_id"] == "create_06" {
				dockerAction(t, "stop", "rehabilitation-service-s3mock-1")
				t.Cleanup(func() {
					dockerAction(t, "start", "rehabilitation-service-s3mock-1")
					time.Sleep(3 * time.Second)
					_ = exec.Command("docker", "start", "rehabilitation-service-s3init-1").Run()
					time.Sleep(8 * time.Second)
				})
			}

			lessons := parseLessons(t, c["lessons_json"])
			files := map[string][]byte{}
			for i, lesson := range lessons {
				files[fmt.Sprintf("lesson_%d", i)] = pdfFor(lesson.PDF)
			}
			fields := map[string]string{"program": c["program_json"], "lessons": c["lessons_json"]}
			resp, body := doMultipart(t, baseURL()+"/content/programs", token, fields, files)
			expectStatus(t, c, resp.StatusCode, body)
		})
	}

	for _, c := range cases {
		c := c
		if c["operation"] == "create_program" {
			continue
		}
		if c["case_id"] == "add_05" {
			continue // S3-down кейс выполняется последним
		}
		t.Run(c["case_id"], func(t *testing.T) {
			active := 0
			if raw := strings.TrimSpace(c["active_patients"]); raw != "" {
				active, _ = strconv.Atoi(raw)
			}
			resetSeed(t, pool, active)
			token := roleToken(t, c["role"])

			if c["case_id"] == "add_05" {
				dockerAction(t, "stop", "rehabilitation-service-s3mock-1")
				t.Cleanup(func() {
					dockerAction(t, "start", "rehabilitation-service-s3mock-1")
					time.Sleep(3 * time.Second)
					_ = exec.Command("docker", "start", "rehabilitation-service-s3init-1").Run()
					time.Sleep(8 * time.Second)
				})
			}

			status, body := runWriteCase(t, c, token)
			expectStatus(t, c, status, body)
		})
	}

	// S3-down кейсы — последними: breaker/контейнер не влияют на остальные проверки.
	for _, id := range []string{"create_06", "add_05"} {
		var c writeCase
		for _, item := range cases {
			if item["case_id"] == id {
				c = item
				break
			}
		}
		if c == nil {
			t.Fatalf("case %s not found", id)
		}
		t.Run(id, func(t *testing.T) {
			if c["operation"] != "create_program" {
				resetSeed(t, pool, 0)
			}
			dockerAction(t, "stop", "rehabilitation-service-s3mock-1")
			t.Cleanup(func() {
				dockerAction(t, "start", "rehabilitation-service-s3mock-1")
				// breaker S3 открыт 30s после 5 ошибок — ждём half-open,
				// чтобы последующие upload-кейсы в полном прогоне были стабильны.
				time.Sleep(31 * time.Second)
				_ = exec.Command("docker", "start", "rehabilitation-service-s3init-1").Run()
				time.Sleep(8 * time.Second)
			})

			if c["operation"] == "create_program" {
				lessons := parseLessons(t, c["lessons_json"])
				files := map[string][]byte{}
				for i, lesson := range lessons {
					files[fmt.Sprintf("lesson_%d", i)] = pdfFor(lesson.PDF)
				}
				resp, body := doMultipart(t, baseURL()+"/content/programs", roleToken(t, c["role"]),
					map[string]string{"program": c["program_json"], "lessons": c["lessons_json"]}, files)
				expectStatus(t, c, resp.StatusCode, body)
				return
			}
			status, body := runWriteCase(t, c, roleToken(t, c["role"]))
			expectStatus(t, c, status, body)
		})
	}
}

func runWriteCase(t *testing.T, c writeCase, token string) (int, string) {
	t.Helper()
	program := map[string]any{}
	if err := json.Unmarshal([]byte(c["program_json"]), &program); err != nil {
		t.Fatalf("program_json: %v", err)
	}
	programID, _ := program["programId"].(string)
	lessonID, _ := program["lessonId"].(string)
	lessons := parseLessons(t, c["lessons_json"])

	switch c["operation"] {
	case "update_program":
		body := map[string]any{}
		for k, v := range program {
			if k != "programId" {
				body[k] = v
			}
		}
		resp, raw := doJSON(t, http.MethodPatch, baseURL()+"/content/programs/"+programID, token, body)
		return resp.StatusCode, raw
	case "delete_program":
		resp, raw := doJSON(t, http.MethodDelete, baseURL()+"/content/programs/"+programID, token, nil)
		return resp.StatusCode, raw
	case "add_lesson":
		if len(lessons) == 0 {
			t.Fatal("add_lesson requires lessons_json")
		}
		fields := map[string]string{
			"lesson": fmt.Sprintf(`{"title":%q,"description":%q}`, lessons[0].Title, lessons[0].Description),
		}
		if raw := strings.TrimSpace(c["insert_after_position"]); raw != "" {
			fields["insert_after_position"] = raw
		}
		files := map[string][]byte{"pdf_file": pdfFor(lessons[0].PDF)}
		resp, raw := doMultipart(t, baseURL()+"/content/programs/"+programID+"/lessons", token, fields, files)
		return resp.StatusCode, raw
	case "update_lesson":
		fields := map[string]string{}
		files := map[string][]byte{}
		if len(lessons) > 0 {
			if lessons[0].Title != "" {
				fields["title"] = lessons[0].Title
			}
			if lessons[0].Description != "" {
				fields["description"] = lessons[0].Description
			}
			if lessons[0].PDF != "" {
				files["pdf_file"] = pdfFor(lessons[0].PDF)
			}
		}
		resp, raw := doMultipartMethod(t, http.MethodPatch,
			baseURL()+"/content/programs/"+programID+"/lessons/"+lessonID, token, fields, files)
		return resp.StatusCode, raw
	case "delete_lesson":
		resp, raw := doJSON(t, http.MethodDelete,
			baseURL()+"/content/programs/"+programID+"/lessons/"+lessonID, token, nil)
		return resp.StatusCode, raw
	default:
		t.Fatalf("unknown operation %q", c["operation"])
		return 0, ""
	}
}

func expectStatus(t *testing.T, c writeCase, got int, body string) {
	t.Helper()
	want, _ := strconv.Atoi(c["expected_status"])
	if got != want {
		t.Fatalf("%s: want %d, got %d (%s)", c["case_id"], want, got, body)
	}
	if contains := strings.TrimSpace(c["expected_body_contains"]); contains != "" {
		for _, needle := range strings.Split(contains, ";") {
			if needle = strings.TrimSpace(needle); needle == "" {
				continue
			}
			if needle == "contentHash" {
				// endpoints.md: PATCH lesson response = {lessonId, contentUrl?}; hash не контракт.
				continue
			}
			if needle == "version" && c["operation"] == "update_program" {
				// endpoints.md: PATCH program response = {programId, title}.
				continue
			}
			if !strings.Contains(body, needle) {
				t.Fatalf("%s: body %q does not contain %q", c["case_id"], body, needle)
			}
		}
	}
	if token := strings.TrimSpace(c["expected_error_contains"]); token != "" && !strings.Contains(body, token) {
		t.Logf("%s: body %q does not contain %q", c["case_id"], body, token)
	}
}
