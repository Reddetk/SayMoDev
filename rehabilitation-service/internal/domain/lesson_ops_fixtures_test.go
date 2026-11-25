package domain

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func parsePositions(t *testing.T, raw string) []int {
	t.Helper()
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ";")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			t.Fatalf("bad position in fixture: %q", p)
		}
		out = append(out, n)
	}
	return out
}

func mustClassifier(t *testing.T) Classifier {
	t.Helper()
	c, err := NewClassifier("easy", "motor")
	if err != nil {
		t.Fatalf("classifier: %v", err)
	}
	return c
}

func validDraft(position int, title string) LessonDraft {
	sum := sha256.Sum256([]byte(strconv.Itoa(position)))
	return LessonDraft{
		Title:         title,
		OrderPosition: position,
		ContentHash:   fmt.Sprintf("%x", sum),
		ContentURL:    fmt.Sprintf("https://cdn.saymo.example/lessons/%d.pdf", position),
		FileSizeBytes: 1024,
		MimeType:      "application/pdf",
	}
}

func restoreProgram(t *testing.T, positions []int, version int) *Program {
	t.Helper()
	lessons := make([]Lesson, 0, len(positions))
	for _, pos := range positions {
		lesson, err := NewLesson(validDraft(pos, fmt.Sprintf("Занятие %d", pos)))
		if err != nil {
			t.Fatalf("lesson %d: %v", pos, err)
		}
		lessons = append(lessons, lesson)
	}

	program, err := RestoreProgram(uuid.Nil, "Восстановленный курс", mustClassifier(t), version, 0, lessons, fixedNow, fixedNow)
	if err != nil {
		t.Fatalf("restore program: %v", err)
	}
	return program
}

// TestLessonOpsFixtures — таблица testdata/fixtures/lesson_ops.csv.
func TestLessonOpsFixtures(t *testing.T) {
	for _, row := range readFixture(t, "lesson_ops.csv") {
		row := row
		t.Run(row["case_id"], func(t *testing.T) {
			positions := parsePositions(t, row["existing_positions"])
			version, err := strconv.Atoi(strings.TrimSpace(row["current_version"]))
			if err != nil {
				t.Fatalf("current_version: %v", err)
			}

			wantValid := row["expected_valid"] == "true"
			token := row["expected_error_contains"]
			newTitle := row["new_title"]

			var program *Program
			if version == 0 {
				// Программы ещё нет: append — это создание, домен обязан отказать.
				_, err := NewProgram("Пустая программа", "easy", "motor", nil, fixedNow)
				assertErrToken(t, err, token, wantValid)
				return
			}
			program = restoreProgram(t, positions, version)

			insertAfter := 0
			if raw := strings.TrimSpace(row["insert_after_position"]); raw != "" {
				insertAfter, err = strconv.Atoi(raw)
				if err != nil {
					t.Fatalf("insert_after_position: %v", err)
				}
			}

			switch row["op"] {
			case "append":
				_, err = program.AppendLesson(validDraft(len(positions)+1, newTitle), fixedNow)
			case "insert":
				_, err = program.InsertLessonAfter(insertAfter, validDraft(insertAfter+1, newTitle), fixedNow)
			case "rename":
				err = program.Rename(newTitle, fixedNow)
			default:
				t.Fatalf("unknown op %q", row["op"])
			}

			if !wantValid {
				assertErrToken(t, err, token, wantValid)
				return
			}
			if err != nil {
				t.Fatalf("want valid op, got %v", err)
			}

			wantVersion, err := strconv.Atoi(strings.TrimSpace(row["expected_new_version"]))
			if err != nil {
				t.Fatalf("expected_new_version: %v", err)
			}
			if program.Version() != wantVersion {
				t.Fatalf("version: want %d, got %d", wantVersion, program.Version())
			}
			wantPositions := parsePositions(t, row["expected_positions"])
			if got := positionsOf(program.Lessons()); !equalInts(got, wantPositions) {
				t.Fatalf("positions: want %v, got %v", wantPositions, got)
			}
		})
	}
}

func assertErrToken(t *testing.T, err error, token string, wantValid bool) {
	t.Helper()
	if wantValid {
		return
	}
	if err == nil {
		t.Fatalf("want error containing %q, got nil", token)
	}
	if !strings.Contains(err.Error(), token) {
		t.Fatalf("want error containing %q, got %q", token, err.Error())
	}
}
