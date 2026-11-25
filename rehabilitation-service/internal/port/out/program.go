// Package out — вторичные порты BC#3. DTO принадлежат пакету порта (ADR-007).
// goca:manual — ADR-007 port-owned DTOs; goca repository генерирует domain-типы.
package out

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound — запись отсутствует.
var ErrNotFound = errors.New("not found")

// ProgramFilter — фильтры публичного каталога.
type ProgramFilter struct {
	Difficulty  string
	AphasiaType string
}

// ProgramSummary — строка каталога (без уроков).
type ProgramSummary struct {
	ProgramID    string    `json:"programId"`
	Title        string    `json:"title"`
	Difficulty   string    `json:"difficulty"`
	AphasiaType  string    `json:"aphasiaType"`
	Version      int       `json:"version"`
	TotalLessons int       `json:"total_lessons"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// LessonRecord — урок в ответе карточки программы.
type LessonRecord struct {
	LessonID      string    `json:"lessonId"`
	OrderPosition int       `json:"orderPosition"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	ContentURL    string    `json:"contentUrl"`
	ContentHash   string    `json:"contentHash"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// ProgramRecord — программа с уроками выбранной версии.
type ProgramRecord struct {
	ProgramID   string         `json:"programId"`
	Title       string         `json:"title"`
	Difficulty  string         `json:"difficulty"`
	AphasiaType string         `json:"aphasiaType"`
	Version     int            `json:"version"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
	Lessons     []LessonRecord `json:"lessons"`
}

// ProgramRepository — чтение каталога BC#3.
type ProgramRepository interface {
	List(ctx context.Context, filter ProgramFilter) ([]ProgramSummary, error)
	Get(ctx context.Context, programID string, version int) (ProgramRecord, error)
	GetLesson(ctx context.Context, programID, lessonID string) (LessonRecord, error)
}

// Presigner — генерация presigned GET URL для PDF.
type Presigner interface {
	PresignGet(ctx context.Context, contentURL string, ttl time.Duration) (string, error)
}
