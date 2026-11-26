// goca:manual — write-DTO и порт записи BC#3 (ADR-007).
package out

import (
	"context"
	"errors"
)

var ErrConflict = errors.New("conflict")
var ErrUnavailable = errors.New("dependency unavailable")
var ErrInvalidPosition = errors.New("invalid insert position")

// LessonWrite — поля нового/обновляемого урока.
type LessonWrite struct {
	ID            string
	Title         string
	Description   string
	ContentURL    string
	ContentHash   string
	FileSizeBytes int64
	MimeType      string
	UploadedBy    *string
}

// CreateProgramCmd — атомарное создание программы с уроками.
type CreateProgramCmd struct {
	ID          string
	Title       string
	Difficulty  string
	AphasiaType string
	Lessons     []LessonWrite
}

// LessonPatch — частичное обновление урока; nil = не менять.
type LessonPatch struct {
	Title         *string
	Description   *string
	ContentURL    *string
	ContentHash   *string
	FileSizeBytes *int64
	MimeType      *string
}

// ProgramWriter — транзакционная запись BC#3.
type ProgramWriter interface {
	Create(ctx context.Context, cmd CreateProgramCmd) (ProgramRecord, error)
	AppendLesson(ctx context.Context, programID string, lesson LessonWrite) (LessonRecord, int, error)
	InsertLessonAfter(ctx context.Context, programID string, after int, lesson LessonWrite) (LessonRecord, int, error)
	Rename(ctx context.Context, programID, title string) error
	DeleteProgram(ctx context.Context, programID string) error
	UpdateLesson(ctx context.Context, programID, lessonID string, patch LessonPatch) (LessonRecord, error)
	DeleteLesson(ctx context.Context, programID, lessonID string) error
	// FindContentByHash — дедупликация PDF: существующий content_url для SHA-256.
	FindContentByHash(ctx context.Context, contentHash string) (string, bool, error)
	CurrentVersion(ctx context.Context, programID string) (int, error)
}
