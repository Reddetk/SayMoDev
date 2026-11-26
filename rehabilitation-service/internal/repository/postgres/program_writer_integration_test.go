//go:build integration

package postgres

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/port/out"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testLesson(title string) out.LessonWrite {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(title)))
	return out.LessonWrite{
		Title:         title,
		Description:   "integration test",
		ContentURL:    "http://localhost:9005/saymo-programs/integration/" + hash + ".pdf",
		ContentHash:   hash,
		FileSizeBytes: 1024,
		MimeType:      "application/pdf",
	}
}

func TestProgramWriterIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	repo := NewProgramRepository(pool)
	classifier := "it_" + uuid.NewString()[:8]

	created, err := repo.Create(ctx, out.CreateProgramCmd{
		Title:       "Интеграционный курс",
		Difficulty:  "easy",
		AphasiaType: classifier,
		Lessons:     []out.LessonWrite{testLesson("Урок 1"), testLesson("Урок 2")},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = repo.DeleteProgram(context.Background(), created.ProgramID) })

	if created.Version != 1 || len(created.Lessons) != 2 {
		t.Fatalf("created: version=%d lessons=%d", created.Version, len(created.Lessons))
	}

	appended, version, err := repo.AppendLesson(ctx, created.ProgramID, testLesson("Урок 3"))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if version != 1 || appended.OrderPosition != 3 {
		t.Fatalf("append: version=%d position=%d", version, appended.OrderPosition)
	}

	inserted, version, err := repo.InsertLessonAfter(ctx, created.ProgramID, 1, testLesson("Вставка"))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if version != 2 || inserted.OrderPosition != 2 {
		t.Fatalf("insert: version=%d position=%d", version, inserted.OrderPosition)
	}

	record, err := repo.Get(ctx, created.ProgramID, 0)
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if record.Version != 2 || len(record.Lessons) != 4 {
		t.Fatalf("latest: version=%d lessons=%d", record.Version, len(record.Lessons))
	}

	// Grandfathering: уроки позиций 1 и 2 сохраняют lesson_id в новой версии.
	if record.Lessons[0].LessonID != created.Lessons[0].LessonID ||
		record.Lessons[2].LessonID != created.Lessons[1].LessonID {
		t.Fatal("lesson ids must be stable across versions")
	}

	old, err := repo.Get(ctx, created.ProgramID, 1)
	if err != nil {
		t.Fatalf("get v1: %v", err)
	}
	if old.Version != 1 || len(old.Lessons) != 3 {
		t.Fatalf("v1: version=%d lessons=%d", old.Version, len(old.Lessons))
	}

	newTitle := "Обновлённый урок"
	updated, err := repo.UpdateLesson(ctx, created.ProgramID, created.Lessons[0].LessonID, out.LessonPatch{Title: &newTitle})
	if err != nil {
		t.Fatalf("update lesson: %v", err)
	}
	if updated.Title != newTitle {
		t.Fatalf("update lesson title: %q", updated.Title)
	}

	if err := repo.Rename(ctx, created.ProgramID, "Переименованный курс"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	if err := repo.DeleteLesson(ctx, created.ProgramID, created.Lessons[1].LessonID); err != nil {
		t.Fatalf("delete lesson: %v", err)
	}
	afterDelete, err := repo.Get(ctx, created.ProgramID, 2)
	if err != nil {
		t.Fatalf("get after lesson delete: %v", err)
	}
	if len(afterDelete.Lessons) != 3 {
		t.Fatalf("lessons after delete: %d", len(afterDelete.Lessons))
	}
}
