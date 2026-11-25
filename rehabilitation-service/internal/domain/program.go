package domain

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Program — агрегат BC#3: программа с неизменяемым классификатором и
// версионируемым набором уроков. Уроки текущей версии живут внутри агрегата.
type Program struct {
	id             uuid.UUID
	title          string
	classifier     Classifier
	version        int
	activeRefCount int
	lessons        []Lesson
	createdAt      time.Time
	updatedAt      time.Time
}

// NewProgram создаёт программу минимум с одним уроком.
// Порядок уроков: либо все OrderPosition = 0 (назначаются 1..N по порядку),
// либо заданы ровно 1..N без дыр и дублей.
func NewProgram(title, difficulty, aphasiaType string, drafts []LessonDraft, now time.Time) (*Program, error) {
	if strings.TrimSpace(title) == "" || utf8.RuneCountInString(title) > maxTitleRunes {
		return nil, ErrProgramTitle
	}

	classifier, err := NewClassifier(difficulty, aphasiaType)
	if err != nil {
		return nil, err
	}

	if len(drafts) == 0 || len(drafts) > maxLessons {
		return nil, ErrLessons
	}

	positions, err := normalizePositions(drafts)
	if err != nil {
		return nil, err
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}

	program := &Program{
		id:         uuid.New(),
		title:      title,
		classifier: classifier,
		version:    1,
		lessons:    make([]Lesson, 0, len(drafts)),
		createdAt:  now,
		updatedAt:  now,
	}

	for i, draft := range drafts {
		draft.OrderPosition = positions[i]
		lesson, err := newLesson(draft, now)
		if err != nil {
			return nil, err
		}
		lesson.programID = program.id
		lesson.programVersion = program.version
		lesson.createdAt = now
		lesson.updatedAt = now
		program.lessons = append(program.lessons, lesson)
	}

	return program, nil
}

// RestoreProgram восстанавливает агрегат из хранилища, не пересоздавая уроки.
func RestoreProgram(
	id uuid.UUID,
	title string,
	classifier Classifier,
	version, activeRefCount int,
	lessons []Lesson,
	createdAt, updatedAt time.Time,
) (*Program, error) {
	if strings.TrimSpace(title) == "" || utf8.RuneCountInString(title) > maxTitleRunes {
		return nil, ErrProgramTitle
	}
	if version < 1 {
		return nil, ErrProgramVersion
	}
	if activeRefCount < 0 {
		return nil, ErrReferenceCount
	}
	if len(lessons) == 0 || len(lessons) > maxLessons {
		return nil, ErrLessons
	}
	if err := validateRestoredPositions(lessons); err != nil {
		return nil, err
	}

	restored := make([]Lesson, len(lessons))
	for i, lesson := range lessons {
		lesson.programID = id
		lesson.programVersion = version
		restored[i] = lesson
	}

	return &Program{
		id:             id,
		title:          title,
		classifier:     classifier,
		version:        version,
		activeRefCount: activeRefCount,
		lessons:        restored,
		createdAt:      createdAt,
		updatedAt:      updatedAt,
	}, nil
}

// AppendLesson добавляет урок в конец, не меняя версию программы.
func (p *Program) AppendLesson(draft LessonDraft, now time.Time) (Lesson, error) {
	if len(p.lessons) == 0 || len(p.lessons) >= maxLessons {
		return Lesson{}, ErrLessons
	}

	draft.OrderPosition = len(p.lessons) + 1
	lesson, err := newLesson(draft, now)
	if err != nil {
		return Lesson{}, err
	}
	lesson.programID = p.id
	lesson.programVersion = p.version
	p.lessons = append(p.lessons, lesson)
	p.touch(now)

	return lesson, nil
}

// InsertLessonAfter вставляет урок после позиции K < N.
// Контракт: K >= total_lessons → 400; валидная вставка между уроками
// увеличивает версию и сдвигает K+1..N (snapshot isolation для старых пациентов).
func (p *Program) InsertLessonAfter(position int, draft LessonDraft, now time.Time) (Lesson, error) {
	if len(p.lessons) == 0 || len(p.lessons) >= maxLessons {
		return Lesson{}, ErrLessons
	}
	if position < 1 || position >= len(p.lessons) {
		return Lesson{}, ErrInsertAfterPosition
	}

	draft.OrderPosition = position + 1
	lesson, err := newLesson(draft, now)
	if err != nil {
		return Lesson{}, err
	}

	newVersion := p.version + 1
	lesson.programID = p.id
	lesson.programVersion = newVersion

	shifted := make([]Lesson, 0, len(p.lessons)+1)
	for _, existing := range p.lessons {
		if existing.orderPosition > position {
			existing.orderPosition++
		}
		existing.programVersion = newVersion
		shifted = append(shifted, existing)
	}
	shifted = append(shifted, lesson)

	// Сохраняем порядок по order_position (1..N+1).
	for i := 1; i < len(shifted); i++ {
		for j := i; j > 0 && shifted[j].orderPosition < shifted[j-1].orderPosition; j-- {
			shifted[j], shifted[j-1] = shifted[j-1], shifted[j]
		}
	}

	p.lessons = shifted
	p.version = newVersion
	p.touch(now)

	return lesson, nil
}

// Rename меняет только заголовок программы: классификатор неизменяем.
func (p *Program) Rename(title string, now time.Time) error {
	if strings.TrimSpace(title) == "" || utf8.RuneCountInString(title) > maxTitleRunes {
		return ErrProgramTitle
	}
	p.title = title
	p.touch(now)
	return nil
}

func (p *Program) IncrementReferenceCount() int {
	p.activeRefCount++
	return p.activeRefCount
}

// DecrementReferenceCount не опускается ниже нуля (docs: counter floor 0).
func (p *Program) DecrementReferenceCount() int {
	if p.activeRefCount > 0 {
		p.activeRefCount--
	}
	return p.activeRefCount
}

func (p *Program) touch(now time.Time) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	p.updatedAt = now
}

func (p *Program) ID() uuid.UUID             { return p.id }
func (p *Program) Title() string             { return p.title }
func (p *Program) Classifier() Classifier    { return p.classifier }
func (p *Program) Version() int              { return p.version }
func (p *Program) ActiveReferenceCount() int { return p.activeRefCount }
func (p *Program) CreatedAt() time.Time      { return p.createdAt }
func (p *Program) UpdatedAt() time.Time      { return p.updatedAt }

// Lessons возвращает копию среза: внешний код не может обойти инварианты.
func (p *Program) Lessons() []Lesson {
	out := make([]Lesson, len(p.lessons))
	copy(out, p.lessons)
	for i := range out {
		if out[i].uploadedBy != nil {
			id := *out[i].uploadedBy
			out[i].uploadedBy = &id
		}
	}
	return out
}

func validateRestoredPositions(lessons []Lesson) error {
	seen := make(map[int]struct{}, len(lessons))
	for _, lesson := range lessons {
		if lesson.orderPosition < 1 || lesson.orderPosition > maxLessons {
			return ErrOrderPosition
		}
		if _, dup := seen[lesson.orderPosition]; dup {
			return ErrOrderPosition
		}
		seen[lesson.orderPosition] = struct{}{}
	}
	for i := 1; i <= len(lessons); i++ {
		if _, ok := seen[i]; !ok {
			return ErrOrderPosition
		}
	}
	return nil
}

func normalizePositions(drafts []LessonDraft) ([]int, error) {
	allZero := true
	for _, draft := range drafts {
		if draft.OrderPosition != 0 {
			allZero = false
			break
		}
	}

	positions := make([]int, len(drafts))
	if allZero {
		for i := range positions {
			positions[i] = i + 1
		}
		return positions, nil
	}

	seen := make(map[int]struct{}, len(drafts))
	for _, draft := range drafts {
		if draft.OrderPosition < 1 || draft.OrderPosition > maxLessons {
			return nil, ErrOrderPosition
		}
		if _, dup := seen[draft.OrderPosition]; dup {
			return nil, ErrOrderPosition
		}
		seen[draft.OrderPosition] = struct{}{}
	}
	if len(seen) != len(drafts) {
		return nil, ErrOrderPosition
	}
	for i := 1; i <= len(drafts); i++ {
		if _, ok := seen[i]; !ok {
			return nil, ErrOrderPosition
		}
	}

	for i, draft := range drafts {
		positions[i] = draft.OrderPosition
	}
	return positions, nil
}
