package domain

import "errors"

// Sentinel-ошибки домена BC#3. Тексты содержат имена полей контракта,
// чтобы их можно было сопоставлять с ответами API и фикстурами.
var (
	ErrProgramTitle        = errors.New("program title must be 1..255 characters")
	ErrDifficulty          = errors.New("difficulty must be one of easy|medium|hard")
	ErrAphasiaType         = errors.New("aphasia_type must be 1..50 characters without surrounding spaces")
	ErrLessons             = errors.New("lessons must contain between 1 and 200 items")
	ErrOrderPosition       = errors.New("order_position must be a gapless 1..N sequence within 1..200")
	ErrLessonTitle         = errors.New("lesson title must be 1..255 characters")
	ErrDescription         = errors.New("description must be at most 2000 characters")
	ErrContentURL          = errors.New("content_url must not be empty")
	ErrContentHash         = errors.New("content_hash must be 64 lowercase hex characters")
	ErrMimeType            = errors.New("mime_type must be application/pdf")
	ErrFileSizeBytes       = errors.New("file_size_bytes must be greater than 0")
	ErrInsertAfterPosition = errors.New("insert_after_position must be less than total_lessons and at least 1")
	ErrProgramVersion      = errors.New("program version must be >= 1")
	ErrReferenceCount      = errors.New("active_reference_count must be >= 0")
)
