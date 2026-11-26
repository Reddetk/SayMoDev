package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/port/out"
)

type fakeWriter struct {
	created      out.CreateProgramCmd
	appended     bool
	inserted     bool
	insertAfter  int
	renamed      string
	deleted      bool
	lessonDelete bool
	patched      out.LessonPatch
	existingURL  string
	hasExisting  bool
	createErr    error
}

func (w *fakeWriter) Create(_ context.Context, cmd out.CreateProgramCmd) (out.ProgramRecord, error) {
	w.created = cmd
	if w.createErr != nil {
		return out.ProgramRecord{}, w.createErr
	}
	return out.ProgramRecord{ProgramID: cmd.ID, Version: 1}, nil
}

func (w *fakeWriter) AppendLesson(_ context.Context, _ string, _ out.LessonWrite) (out.LessonRecord, int, error) {
	w.appended = true
	return out.LessonRecord{LessonID: "l1", OrderPosition: 4}, 1, nil
}

func (w *fakeWriter) InsertLessonAfter(_ context.Context, _ string, after int, _ out.LessonWrite) (out.LessonRecord, int, error) {
	w.inserted = true
	w.insertAfter = after
	return out.LessonRecord{LessonID: "l2", OrderPosition: after + 1}, 2, nil
}

func (w *fakeWriter) Rename(_ context.Context, _ string, title string) error {
	w.renamed = title
	return nil
}

func (w *fakeWriter) DeleteProgram(context.Context, string) error { w.deleted = true; return nil }

func (w *fakeWriter) UpdateLesson(_ context.Context, _, _ string, patch out.LessonPatch) (out.LessonRecord, error) {
	w.patched = patch
	return out.LessonRecord{LessonID: "l1"}, nil
}

func (w *fakeWriter) DeleteLesson(context.Context, string, string) error {
	w.lessonDelete = true
	return nil
}

func (w *fakeWriter) FindContentByHash(context.Context, string) (string, bool, error) {
	return w.existingURL, w.hasExisting, nil
}

type fakeUploader struct {
	calls int
}

func (u *fakeUploader) UploadPDF(_ context.Context, _, _ string, content []byte) (string, string, int64, error) {
	u.calls++
	return "https://storage.example/saymo-programs/programs/p/lessons/l/v1.pdf", "hash", int64(len(content)), nil
}

type fakeInvalidator struct {
	all       int
	programID string
}

func (i *fakeInvalidator) InvalidateAll(context.Context) error { i.all++; return nil }

func (i *fakeInvalidator) InvalidateProgram(_ context.Context, programID string) error {
	i.programID = programID
	return nil
}

func pdfBytes(size int) []byte {
	buf := make([]byte, size)
	copy(buf, "%PDF-1.4")
	return buf
}

func TestCreateUsesDedupBeforeUpload(t *testing.T) {
	writer := &fakeWriter{existingURL: "https://storage.example/existing.pdf", hasExisting: true}
	uploader := &fakeUploader{}
	invalidator := &fakeInvalidator{}
	command := NewProgramCommand(writer, uploader, invalidator)

	record, err := command.Create(context.Background(), "Курс", "easy", "motor", []LessonUpload{{Title: "Урок", PDF: pdfBytes(10)}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if uploader.calls != 0 {
		t.Fatalf("dedup must skip upload, calls=%d", uploader.calls)
	}
	if writer.created.Lessons[0].ContentURL != "https://storage.example/existing.pdf" {
		t.Fatalf("content url: %q", writer.created.Lessons[0].ContentURL)
	}
	if invalidator.all != 1 {
		t.Fatalf("invalidate all: %d", invalidator.all)
	}
	if record.ProgramID != writer.created.ID {
		t.Fatal("program id mismatch")
	}
}

func TestCreateRejectsOversizePDF(t *testing.T) {
	command := NewProgramCommand(&fakeWriter{}, &fakeUploader{}, nil)
	_, err := command.Create(context.Background(), "Курс", "easy", "motor",
		[]LessonUpload{{Title: "Урок", PDF: pdfBytes(maxPDFBytes + 1)}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput, got %v", err)
	}
}

func TestAddLessonAppendAndInsert(t *testing.T) {
	writer := &fakeWriter{}
	command := NewProgramCommand(writer, &fakeUploader{}, &fakeInvalidator{})

	if _, version, err := command.AddLesson(context.Background(), "p1", LessonUpload{Title: "Новый", PDF: pdfBytes(4)}, nil); err != nil || version != 1 || !writer.appended {
		t.Fatalf("append: v=%d appended=%v err=%v", version, writer.appended, err)
	}

	after := 1
	if _, version, err := command.AddLesson(context.Background(), "p1", LessonUpload{Title: "Вставка", PDF: pdfBytes(4)}, &after); err != nil || version != 2 || !writer.inserted || writer.insertAfter != 1 {
		t.Fatalf("insert: v=%d inserted=%v after=%d err=%v", version, writer.inserted, writer.insertAfter, err)
	}
}

func TestUpdateLessonRequiresFields(t *testing.T) {
	command := NewProgramCommand(&fakeWriter{}, &fakeUploader{}, nil)
	if _, err := command.UpdateLesson(context.Background(), "p1", "l1", out.LessonPatch{}, nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput, got %v", err)
	}
}

func TestDeleteInvalidatesCaches(t *testing.T) {
	invalidator := &fakeInvalidator{}
	command := NewProgramCommand(&fakeWriter{}, &fakeUploader{}, invalidator)

	if err := command.DeleteProgram(context.Background(), "p1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if invalidator.programID != "p1" || invalidator.all != 1 {
		t.Fatalf("invalidate: program=%q all=%d", invalidator.programID, invalidator.all)
	}
}
