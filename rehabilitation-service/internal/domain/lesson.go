package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	maxTitleRunes       = 255
	maxDescriptionRunes = 2000
	maxLessons          = 200
	pdfMimeType         = "application/pdf"
)

var contentHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// LessonDraft — входные данные урока до валидации.
type LessonDraft struct {
	Title         string
	Description   string
	ContentURL    string
	ContentHash   string
	FileSizeBytes int64
	MimeType      string
	UploadedBy    *uuid.UUID
	// OrderPosition задаётся при создании программы; 0 означает «назначить по порядку».
	OrderPosition int
}

// Lesson — урок в рамках конкретной версии программы.
type Lesson struct {
	id             uuid.UUID
	programID      uuid.UUID
	programVersion int
	orderPosition  int
	title          string
	description    string
	contentURL     string
	contentHash    string
	fileSizeBytes  int64
	mimeType       string
	uploadedBy     *uuid.UUID
	createdAt      time.Time
	updatedAt      time.Time
}

// NewLesson валидирует поля урока и выдаёт новый идентификатор.
func NewLesson(draft LessonDraft) (Lesson, error) {
	return newLesson(draft, time.Now().UTC())
}

func newLesson(draft LessonDraft, now time.Time) (Lesson, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if draft.OrderPosition < 1 || draft.OrderPosition > maxLessons {
		return Lesson{}, ErrOrderPosition
	}
	if strings.TrimSpace(draft.Title) == "" || utf8.RuneCountInString(draft.Title) > maxTitleRunes {
		return Lesson{}, ErrLessonTitle
	}
	if utf8.RuneCountInString(draft.Description) > maxDescriptionRunes {
		return Lesson{}, ErrDescription
	}
	if strings.TrimSpace(draft.ContentURL) == "" {
		return Lesson{}, ErrContentURL
	}
	if !contentHashPattern.MatchString(draft.ContentHash) {
		return Lesson{}, ErrContentHash
	}
	if draft.FileSizeBytes <= 0 {
		return Lesson{}, ErrFileSizeBytes
	}
	if draft.MimeType != pdfMimeType {
		return Lesson{}, ErrMimeType
	}

	return Lesson{
		id:            uuid.New(),
		orderPosition: draft.OrderPosition,
		title:         draft.Title,
		description:   draft.Description,
		contentURL:    draft.ContentURL,
		contentHash:   draft.ContentHash,
		fileSizeBytes: draft.FileSizeBytes,
		mimeType:      draft.MimeType,
		uploadedBy:    draft.UploadedBy,
		createdAt:     now,
		updatedAt:     now,
	}, nil
}

func (l Lesson) ID() uuid.UUID        { return l.id }
func (l Lesson) ProgramID() uuid.UUID { return l.programID }
func (l Lesson) ProgramVersion() int  { return l.programVersion }
func (l Lesson) OrderPosition() int   { return l.orderPosition }
func (l Lesson) Title() string        { return l.title }
func (l Lesson) Description() string  { return l.description }
func (l Lesson) ContentURL() string   { return l.contentURL }
func (l Lesson) ContentHash() string  { return l.contentHash }
func (l Lesson) FileSizeBytes() int64 { return l.fileSizeBytes }
func (l Lesson) MimeType() string     { return l.mimeType }
func (l Lesson) UploadedBy() *uuid.UUID {
	if l.uploadedBy == nil {
		return nil
	}
	copyID := *l.uploadedBy
	return &copyID
}
func (l Lesson) CreatedAt() time.Time { return l.createdAt }
func (l Lesson) UpdatedAt() time.Time { return l.updatedAt }
