package valobj

import (
	"github.com/Reddetk/SayMoDev/identy-service/internal/core/consts"
	corerr "github.com/Reddetk/SayMoDev/identy-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/identy-service/internal/port/in"
)

// Classifier represents immutable tuple (difficulty, aphasiaType)
// Cross-BC VO: used in BC#1 (registration), BC#3 (program), BC#4 (activation)
type Classifier struct {
	difficulty  string
	aphasiaType string
}

func validateClassifier(difficulty, aphasiaType string) error {
	if difficulty == "" {
		return corerr.ErrClassifierDifficultyRequired
	}
	switch difficulty {
	case consts.DifficultyEasy, consts.DifficultyMedium, consts.DifficultyHard:
	default:
		return corerr.ErrClassifierDifficultyInvalid
	}

	if aphasiaType == "" {
		return corerr.ErrClassifierAphasiaRequired
	}
	switch aphasiaType {
	case consts.AphasiaTypeMotor:
	default:
		return corerr.ErrClassifierAphasiaInvalid
	}

	return nil
}

func NewClassifier(difficulty, aphasiaType string) (Classifier, error) {
	if err := validateClassifier(difficulty, aphasiaType); err != nil {
		return Classifier{}, err
	}
	return Classifier{difficulty: difficulty, aphasiaType: aphasiaType}, nil
}

func MapClassifier(classifierDTO in.ClassifierDTO) (Classifier, error) {
	return NewClassifier(classifierDTO.Difficulty, classifierDTO.AphasiaType)
}

func (c Classifier) Difficulty() string  { return c.difficulty }
func (c Classifier) AphasiaType() string { return c.aphasiaType }

func (c Classifier) Equals(other Classifier) bool {
	return c.difficulty == other.difficulty && c.aphasiaType == other.aphasiaType
}
