package domain

import (
	"strings"
	"unicode/utf8"
)

// Classifier — неизменяемый кортеж (difficulty, aphasia_type).
// Допустимые difficulty зафиксированы docs (easy|medium|hard); список aphasia_type
// открыт и проверяется структурно: ADR-008 оставляет доменную семантику BC#3,
// но полный перечень значений в документации не опубликован.
type Classifier struct {
	difficulty  string
	aphasiaType string
}

func NewClassifier(difficulty, aphasiaType string) (Classifier, error) {
	switch difficulty {
	case "easy", "medium", "hard":
	default:
		return Classifier{}, ErrDifficulty
	}

	if aphasiaType == "" || aphasiaType != strings.TrimSpace(aphasiaType) || utf8.RuneCountInString(aphasiaType) > 50 {
		return Classifier{}, ErrAphasiaType
	}

	return Classifier{difficulty: difficulty, aphasiaType: aphasiaType}, nil
}

func (c Classifier) Difficulty() string  { return c.difficulty }
func (c Classifier) AphasiaType() string { return c.aphasiaType }

func (c Classifier) Equal(other Classifier) bool {
	return c.difficulty == other.difficulty && c.aphasiaType == other.aphasiaType
}
