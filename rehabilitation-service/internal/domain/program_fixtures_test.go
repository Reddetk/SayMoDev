package domain

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

var fixedNow = time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

func readFixture(t *testing.T, name string) []map[string]string {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "fixtures", name)
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture %s: %v", name, err)
	}
	defer f.Close()

	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	if len(rows) < 2 {
		t.Fatalf("fixture %s has no cases", name)
	}

	header := map[string]int{}
	for i, h := range rows[0] {
		header[h] = i
	}

	out := make([]map[string]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		rec := make(map[string]string, len(header))
		for name, idx := range header {
			rec[name] = row[idx]
		}
		out = append(out, rec)
	}
	return out
}

type lessonJSON struct {
	Title         string  `json:"title"`
	Description   string  `json:"description"`
	OrderPosition int     `json:"order_position"`
	ContentHash   string  `json:"content_hash"`
	ContentURL    string  `json:"content_url"`
	FileSizeBytes int64   `json:"file_size_bytes"`
	MimeType      string  `json:"mime_type"`
	UploadedBy    *string `json:"uploaded_by"`
}

func parseLessonJSON(t *testing.T, raw string) []LessonDraft {
	t.Helper()
	var payload []lessonJSON
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("lessons_json: %v", err)
	}
	drafts := make([]LessonDraft, 0, len(payload))
	for _, l := range payload {
		draft := LessonDraft{
			Title:         l.Title,
			Description:   l.Description,
			OrderPosition: l.OrderPosition,
			ContentHash:   l.ContentHash,
			ContentURL:    l.ContentURL,
			FileSizeBytes: l.FileSizeBytes,
			MimeType:      l.MimeType,
		}
		if l.UploadedBy != nil {
			id, err := uuid.Parse(*l.UploadedBy)
			if err != nil {
				t.Fatalf("uploaded_by: %v", err)
			}
			draft.UploadedBy = &id
		}
		drafts = append(drafts, draft)
	}
	return drafts
}

func positionsOf(lessons []Lesson) []int {
	out := make([]int, 0, len(lessons))
	for _, l := range lessons {
		out = append(out, l.OrderPosition())
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestNewProgramFixtures — таблица testdata/fixtures/program.csv от чистого субагента.
func TestNewProgramFixtures(t *testing.T) {
	for _, row := range readFixture(t, "program.csv") {
		row := row
		t.Run(row["case_id"], func(t *testing.T) {
			drafts := parseLessonJSON(t, row["lessons_json"])

			program, err := NewProgram(row["title"], row["difficulty"], row["aphasia_type"], drafts, fixedNow)
			wantValid := row["expected_valid"] == "true"

			if !wantValid {
				if err == nil {
					t.Fatalf("want error containing %q, got valid program", row["expected_error_contains"])
				}
				if token := row["expected_error_contains"]; !strings.Contains(err.Error(), token) {
					t.Fatalf("want error containing %q, got %q", token, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("want valid program, got %v", err)
			}

			var want struct {
				Version   int   `json:"version"`
				Positions []int `json:"positions"`
			}
			if raw := row["expected_result_json"]; raw != "" {
				if err := json.Unmarshal([]byte(raw), &want); err != nil {
					t.Fatalf("expected_result_json: %v", err)
				}
			}
			if program.Version() != want.Version {
				t.Fatalf("version: want %d, got %d", want.Version, program.Version())
			}
			if got := positionsOf(program.Lessons()); !equalInts(got, want.Positions) {
				t.Fatalf("positions: want %v, got %v", want.Positions, got)
			}
		})
	}
}
