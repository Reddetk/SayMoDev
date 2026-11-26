// goca:manual — write-сценарии BC#3: dedup PDF, S3 до транзакции, инвалидация кэша.
package usecase

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/domain"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/port/out"
	"github.com/google/uuid"
)

const maxPDFBytes = 100 << 20

var ErrInvalidInput = errors.New("invalid input")

type PDFUploader interface {
	UploadPDF(ctx context.Context, programID, lessonID string, content []byte) (url, hash string, size int64, err error)
}

type CacheInvalidator interface {
	InvalidateAll(ctx context.Context) error
	InvalidateProgram(ctx context.Context, programID string) error
}

type LessonUpload struct {
	Title       string
	Description string
	PDF         []byte
}

type ProgramCommand struct {
	writer   out.ProgramWriter
	uploader PDFUploader
	cache    CacheInvalidator
	guard    out.ActivePatientsReader
}

func NewProgramCommand(writer out.ProgramWriter, uploader PDFUploader, cache CacheInvalidator, guards ...out.ActivePatientsReader) *ProgramCommand {
	var guard out.ActivePatientsReader
	if len(guards) > 0 {
		guard = guards[0]
	}
	return &ProgramCommand{writer: writer, uploader: uploader, cache: cache, guard: guard}
}

func (c *ProgramCommand) Create(ctx context.Context, title, difficulty, aphasiaType string, lessons []LessonUpload) (out.ProgramRecord, error) {
	if err := validateProgramInput(title, difficulty, aphasiaType, len(lessons)); err != nil {
		return out.ProgramRecord{}, err
	}

	programID := uuid.NewString()
	cmd := out.CreateProgramCmd{
		ID:          programID,
		Title:       title,
		Difficulty:  difficulty,
		AphasiaType: aphasiaType,
		Lessons:     make([]out.LessonWrite, 0, len(lessons)),
	}

	for _, lesson := range lessons {
		prepared, err := c.prepareLesson(ctx, programID, uuid.NewString(), lesson, true)
		if err != nil {
			return out.ProgramRecord{}, err
		}
		cmd.Lessons = append(cmd.Lessons, prepared)
	}

	record, err := c.writer.Create(ctx, cmd)
	if err != nil {
		return out.ProgramRecord{}, err
	}
	c.invalidateAll(ctx)
	return record, nil
}

func (c *ProgramCommand) AddLesson(ctx context.Context, programID string, lesson LessonUpload, insertAfter *int) (out.LessonRecord, int, error) {
	prepared, err := c.prepareLesson(ctx, programID, uuid.NewString(), lesson, true)
	if err != nil {
		return out.LessonRecord{}, 0, err
	}

	var (
		record  out.LessonRecord
		version int
	)
	if insertAfter == nil {
		record, version, err = c.writer.AppendLesson(ctx, programID, prepared)
	} else {
		record, version, err = c.writer.InsertLessonAfter(ctx, programID, *insertAfter, prepared)
	}
	if err != nil {
		return out.LessonRecord{}, 0, err
	}
	c.invalidateProgram(ctx, programID)
	return record, version, nil
}

func (c *ProgramCommand) Rename(ctx context.Context, programID, title string) error {
	if strings.TrimSpace(title) == "" || utf8.RuneCountInString(title) > 255 {
		return fmt.Errorf("%w: title", ErrInvalidInput)
	}
	if err := c.writer.Rename(ctx, programID, title); err != nil {
		return err
	}
	c.invalidateProgram(ctx, programID)
	return nil
}

func (c *ProgramCommand) DeleteProgram(ctx context.Context, programID string) error {
	if err := c.checkActivePatients(ctx, programID); err != nil {
		return err
	}
	if err := c.writer.DeleteProgram(ctx, programID); err != nil {
		return err
	}
	c.invalidateProgram(ctx, programID)
	c.invalidateAll(ctx)
	return nil
}

func (c *ProgramCommand) UpdateLesson(ctx context.Context, programID, lessonID string, patch out.LessonPatch, pdf []byte) (out.LessonRecord, error) {
	if patch.Title == nil && patch.Description == nil && pdf == nil {
		return out.LessonRecord{}, fmt.Errorf("%w: fields", ErrInvalidInput)
	}

	if pdf != nil {
		prepared, err := c.prepareLesson(ctx, programID, lessonID, LessonUpload{PDF: pdf}, false)
		if err != nil {
			return out.LessonRecord{}, err
		}
		patch.ContentURL = &prepared.ContentURL
		patch.ContentHash = &prepared.ContentHash
		patch.FileSizeBytes = &prepared.FileSizeBytes
		patch.MimeType = &prepared.MimeType
	}

	record, err := c.writer.UpdateLesson(ctx, programID, lessonID, patch)
	if err != nil {
		return out.LessonRecord{}, err
	}
	c.invalidateProgram(ctx, programID)
	return record, nil
}

func (c *ProgramCommand) DeleteLesson(ctx context.Context, programID, lessonID string) error {
	if err := c.checkActivePatients(ctx, programID); err != nil {
		return err
	}
	if err := c.writer.DeleteLesson(ctx, programID, lessonID); err != nil {
		return err
	}
	c.invalidateProgram(ctx, programID)
	return nil
}

// checkActivePatients — BC#4 deletion guard. Недоступность BC#4 = fail-closed 503,
// наличие активных пациентов = 409. Без guard (local) остаётся локальный счётчик.
func (c *ProgramCommand) checkActivePatients(ctx context.Context, programID string) error {
	if c.guard == nil {
		return nil
	}
	version, err := c.writer.CurrentVersion(ctx, programID)
	if err != nil {
		return err
	}
	count, err := c.guard.ActivePatients(ctx, programID, version)
	if err != nil {
		return fmt.Errorf("%w: bc4: %v", out.ErrBC4Unavailable, err)
	}
	for v := 1; v < version; v++ {
		older, err := c.guard.ActivePatients(ctx, programID, v)
		if err != nil {
			return fmt.Errorf("%w: bc4 v%d: %v", out.ErrBC4Unavailable, v, err)
		}
		count += older
	}
	if count > 0 {
		return fmt.Errorf("active_patients=%d: %w", count, out.ErrConflict)
	}
	return nil
}

func (c *ProgramCommand) MigratePatients(ctx context.Context, programID string, targetVersion int, selection string, accountIDs []string) (string, int, error) {
	switch selection {
	case "all":
	case "specific":
		if len(accountIDs) == 0 {
			return "", 0, fmt.Errorf("%w: account_ids", ErrInvalidInput)
		}
	default:
		return "", 0, fmt.Errorf("%w: patient_selection", ErrInvalidInput)
	}

	affected := 0
	if selection == "specific" {
		affected = len(accountIDs)
	} else {
		version, err := c.writer.CurrentVersion(ctx, programID)
		if err != nil {
			return "", 0, err
		}
		if c.guard != nil {
			count, err := c.guard.ActivePatients(ctx, programID, version)
			if err != nil {
				return "", 0, fmt.Errorf("%w: bc4: %v", out.ErrBC4Unavailable, err)
			}
			affected = count
		} else {
			count, err := c.writer.ActiveReferenceCount(ctx, programID)
			if err != nil {
				return "", 0, err
			}
			affected = count
		}
	}

	jobID, err := c.writer.CreateMigrationRequest(ctx, programID, targetVersion, selection, accountIDs)
	if err != nil {
		return "", 0, err
	}
	return jobID, affected, nil
}

func (c *ProgramCommand) prepareLesson(ctx context.Context, programID, lessonID string, lesson LessonUpload, requireTitle bool) (out.LessonWrite, error) {
	if len(lesson.PDF) == 0 {
		return out.LessonWrite{}, fmt.Errorf("%w: pdf_file is required", ErrInvalidInput)
	}
	if len(lesson.PDF) > maxPDFBytes {
		return out.LessonWrite{}, fmt.Errorf("%w: pdf_file exceeds 100 MB", ErrInvalidInput)
	}
	if requireTitle && strings.TrimSpace(lesson.Title) == "" {
		return out.LessonWrite{}, fmt.Errorf("%w: title", ErrInvalidInput)
	}
	if !bytes.HasPrefix(lesson.PDF, []byte("%PDF")) {
		return out.LessonWrite{}, fmt.Errorf("%w: fields (pdf_file must be PDF)", ErrInvalidInput)
	}

	hash := fmt.Sprintf("%x", sha256.Sum256(lesson.PDF))
	result := out.LessonWrite{
		ID:            lessonID,
		Title:         lesson.Title,
		Description:   lesson.Description,
		ContentHash:   hash,
		FileSizeBytes: int64(len(lesson.PDF)),
		MimeType:      "application/pdf",
	}

	existingURL, found, err := c.writer.FindContentByHash(ctx, hash)
	if err != nil {
		return out.LessonWrite{}, err
	}
	if found {
		result.ContentURL = existingURL
		return result, nil
	}

	if c.uploader == nil {
		return out.LessonWrite{}, fmt.Errorf("%w: uploader is not configured", ErrInvalidInput)
	}
	url, uploadedHash, size, err := c.uploader.UploadPDF(ctx, programID, lessonID, lesson.PDF)
	if err != nil {
		return out.LessonWrite{}, err
	}
	result.ContentURL = url
	result.ContentHash = uploadedHash
	result.FileSizeBytes = size
	return result, nil
}

func validateProgramInput(title, difficulty, aphasiaType string, lessons int) error {
	if strings.TrimSpace(title) == "" || utf8.RuneCountInString(title) > 255 {
		return fmt.Errorf("%w: title", ErrInvalidInput)
	}
	if _, err := domain.NewClassifier(difficulty, aphasiaType); err != nil {
		return fmt.Errorf("%w: classifier", ErrInvalidInput)
	}
	if lessons < 1 || lessons > 200 {
		return fmt.Errorf("%w: lessons", ErrInvalidInput)
	}
	return nil
}

func (c *ProgramCommand) invalidateAll(ctx context.Context) {
	if c.cache == nil {
		return
	}
	// Инвалидация — после commit, поэтому обрыв клиента не должен её отменять:
	// иначе при отменённом request-ctx stale живёт до TTL.
	invCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_ = c.cache.InvalidateAll(invCtx)
}

func (c *ProgramCommand) invalidateProgram(ctx context.Context, programID string) {
	if c.cache == nil {
		return
	}
	invCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_ = c.cache.InvalidateProgram(invCtx, programID)
}
