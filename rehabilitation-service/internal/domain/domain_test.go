package domain

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func mustProgram(t *testing.T, drafts ...LessonDraft) *Program {
	t.Helper()
	program, err := NewProgram("Курс", "easy", "motor", drafts, fixedNow)
	if err != nil {
		t.Fatalf("NewProgram: %v", err)
	}
	return program
}

func TestProgramGettersExposeAggregateState(t *testing.T) {
	draft := validDraft(1, "Первый урок")
	draft.Description = "Описание"
	draft.UploadedBy = func() *uuid.UUID { id := uuid.New(); return &id }()

	program := mustProgram(t, draft)

	if program.ID() == uuid.Nil {
		t.Fatal("ID must be generated")
	}
	if program.Title() != "Курс" {
		t.Fatalf("Title: %q", program.Title())
	}
	if program.Classifier().Difficulty() != "easy" || program.Classifier().AphasiaType() != "motor" {
		t.Fatalf("Classifier: %+v", program.Classifier())
	}
	if program.Version() != 1 || program.ActiveReferenceCount() != 0 {
		t.Fatalf("version/refs: %d/%d", program.Version(), program.ActiveReferenceCount())
	}
	if !program.CreatedAt().Equal(fixedNow) || !program.UpdatedAt().Equal(fixedNow) {
		t.Fatalf("timestamps: %s / %s", program.CreatedAt(), program.UpdatedAt())
	}

	lesson := program.Lessons()[0]
	if lesson.ID() == uuid.Nil || lesson.ProgramID() != program.ID() {
		t.Fatalf("lesson ids: %s / %s", lesson.ID(), lesson.ProgramID())
	}
	if lesson.ProgramVersion() != 1 || lesson.OrderPosition() != 1 {
		t.Fatalf("lesson version/position: %d/%d", lesson.ProgramVersion(), lesson.OrderPosition())
	}
	if lesson.Title() != "Первый урок" || lesson.Description() != "Описание" {
		t.Fatalf("lesson text: %q / %q", lesson.Title(), lesson.Description())
	}
	if lesson.ContentURL() == "" || lesson.ContentHash() != draft.ContentHash {
		t.Fatalf("lesson content: %q / %q", lesson.ContentURL(), lesson.ContentHash())
	}
	if lesson.FileSizeBytes() != draft.FileSizeBytes || lesson.MimeType() != "application/pdf" {
		t.Fatalf("lesson file: %d / %q", lesson.FileSizeBytes(), lesson.MimeType())
	}
	if lesson.UploadedBy() == nil || *lesson.UploadedBy() != *draft.UploadedBy {
		t.Fatal("UploadedBy must round-trip")
	}
	if lesson.CreatedAt().IsZero() || lesson.UpdatedAt().IsZero() {
		t.Fatal("lesson timestamps must be set")
	}
}

func TestNewProgramAssignsPositionsWhenZero(t *testing.T) {
	lessons := []LessonDraft{validDraft(0, "Один"), validDraft(0, "Два"), validDraft(0, "Три")}
	program := mustProgram(t, lessons...)

	want := []int{1, 2, 3}
	if got := positionsOf(program.Lessons()); !equalInts(got, want) {
		t.Fatalf("positions: want %v, got %v", want, got)
	}
}

func TestLessonsReturnsDefensiveCopy(t *testing.T) {
	program := mustProgram(t, validDraft(1, "Первый"))
	copyOfLessons := program.Lessons()
	copyOfLessons[0] = Lesson{}

	if program.Lessons()[0].Title() != "Первый" {
		t.Fatal("Lessons must return a copy, internal state was mutated")
	}
}

func TestReferenceCountFloorAtZero(t *testing.T) {
	program := mustProgram(t, validDraft(1, "Первый"))

	if got := program.IncrementReferenceCount(); got != 1 {
		t.Fatalf("increment: want 1, got %d", got)
	}
	if got := program.IncrementReferenceCount(); got != 2 {
		t.Fatalf("increment: want 2, got %d", got)
	}
	if got := program.DecrementReferenceCount(); got != 1 {
		t.Fatalf("decrement: want 1, got %d", got)
	}
	if got := program.DecrementReferenceCount(); got != 0 {
		t.Fatalf("decrement: want 0, got %d", got)
	}
	if got := program.DecrementReferenceCount(); got != 0 {
		t.Fatalf("decrement below zero: want 0, got %d", got)
	}
}

func TestRestoreProgramValidation(t *testing.T) {
	classifier := mustClassifier(t)
	lesson, err := NewLesson(validDraft(1, "Первый"))
	if err != nil {
		t.Fatalf("lesson: %v", err)
	}

	cases := []struct {
		name    string
		title   string
		version int
		refs    int
		token   string
	}{
		{name: "empty title", title: "  ", version: 1, token: "title"},
		{name: "zero version", title: "Курс", version: 0, token: "version"},
		{name: "negative refs", title: "Курс", version: 1, refs: -1, token: "active_reference_count"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RestoreProgram(uuid.New(), tc.title, classifier, tc.version, tc.refs, []Lesson{lesson}, fixedNow, fixedNow)
			if err == nil || !strings.Contains(err.Error(), tc.token) {
				t.Fatalf("want error containing %q, got %v", tc.token, err)
			}
		})
	}
}

func TestRenameTrimsNothingAndRejectsEmpty(t *testing.T) {
	program := mustProgram(t, validDraft(1, "Первый"))

	if err := program.Rename("Новое имя", fixedNow); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if program.Title() != "Новое имя" {
		t.Fatalf("title: %q", program.Title())
	}
	if err := program.Rename("   ", fixedNow); err == nil {
		t.Fatal("blank title must be rejected")
	}
}

func TestInsertLessonAfterKeepsLessonIdentities(t *testing.T) {
	program := mustProgram(t, validDraft(1, "Первый"), validDraft(2, "Второй"), validDraft(3, "Третий"))
	before := program.Lessons()

	inserted, err := program.InsertLessonAfter(1, validDraft(2, "Новый"), fixedNow)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if inserted.OrderPosition() != 2 {
		t.Fatalf("inserted position: want 2, got %d", inserted.OrderPosition())
	}
	if program.Version() != 2 {
		t.Fatalf("version: want 2, got %d", program.Version())
	}

	after := program.Lessons()
	wantIDs := []uuid.UUID{before[0].ID(), inserted.ID(), before[1].ID(), before[2].ID()}
	for i, want := range wantIDs {
		if after[i].ID() != want {
			t.Fatalf("lesson %d id: want %s, got %s", i, want, after[i].ID())
		}
		if after[i].OrderPosition() != i+1 {
			t.Fatalf("lesson %d position: want %d, got %d", i, i+1, after[i].OrderPosition())
		}
		if after[i].ProgramVersion() != 2 {
			t.Fatalf("lesson %d program_version: want 2, got %d", i, after[i].ProgramVersion())
		}
	}
}

func TestRestoreRejectsBrokenPositions(t *testing.T) {
	lesson1, err := NewLesson(validDraft(1, "Первый"))
	if err != nil {
		t.Fatalf("lesson: %v", err)
	}
	lesson2, err := NewLesson(validDraft(2, "Второй"))
	if err != nil {
		t.Fatalf("lesson: %v", err)
	}

	cases := []struct {
		name   string
		mutate func([]Lesson) []Lesson
	}{
		{name: "duplicate", mutate: func(l []Lesson) []Lesson { l[1].orderPosition = 1; return l }},
		{name: "gap", mutate: func(l []Lesson) []Lesson { l[1].orderPosition = 3; return l }},
		{name: "out of range", mutate: func(l []Lesson) []Lesson { l[1].orderPosition = 201; return l }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lessons := tc.mutate([]Lesson{lesson1, lesson2})
			if _, err := RestoreProgram(uuid.New(), "Курс", mustClassifier(t), 1, 0, lessons, fixedNow, fixedNow); err == nil {
				t.Fatal("broken positions must be rejected")
			}
		})
	}
}

func TestUploadedByNilReturnsNil(t *testing.T) {
	program := mustProgram(t, validDraft(1, "Первый"))
	if got := program.Lessons()[0].UploadedBy(); got != nil {
		t.Fatalf("want nil uploaded_by, got %v", got)
	}
}
