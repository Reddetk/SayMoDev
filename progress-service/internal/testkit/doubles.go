package testkit

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	corerr "github.com/Reddetk/SayMoDev/progress-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

var catalogNamespace = uuid.MustParse("6f1c2a44-4d7e-4a1e-9a1d-6f2b8e4c9a10")

// ProgramID returns the deterministic program id for a difficulty.
func ProgramID(difficulty string) uuid.UUID {
	return uuid.NewSHA1(catalogNamespace, []byte(difficulty))
}

// LessonID returns the deterministic lesson id for a difficulty/position pair.
func LessonID(difficulty string, position int) uuid.UUID {
	return uuid.NewSHA1(catalogNamespace, []byte(difficulty+"-"+string(rune('0'+position))))
}

// Catalog is an in-memory BC#3 replacement with easy/medium/hard programs (3 lessons each).
type Catalog struct {
	Programs map[uuid.UUID]out.ProgramInfo
	Err      error
}

// NewCatalog builds the three documented programs; lesson 3 is creative.
func NewCatalog() *Catalog {
	c := &Catalog{Programs: map[uuid.UUID]out.ProgramInfo{}}
	for _, difficulty := range []string{"easy", "medium", "hard"} {
		info := out.ProgramInfo{
			ProgramID: ProgramID(difficulty), Version: 1, TotalLessons: 4,
			Difficulty: difficulty, AphasiaType: "motor_aphasia",
		}
		for pos := 1; pos <= 4; pos++ {
			lessonType := "regular"
			if pos == 4 {
				lessonType = "creative"
			}
			info.Lessons = append(info.Lessons, out.LessonInfo{
				LessonID: LessonID(difficulty, pos), OrderPosition: pos,
				LessonType: lessonType, Title: difficulty + " lesson " + string(rune('0'+pos)),
			})
		}
		c.Programs[info.ProgramID] = info
	}
	return c
}

func (c *Catalog) ResolveProgram(ctx context.Context, difficulty, aphasiaType string) (out.ProgramInfo, error) {
	if c.Err != nil {
		return out.ProgramInfo{}, c.Err
	}
	info, ok := c.Programs[ProgramID(difficulty)]
	if !ok || strings.TrimSpace(difficulty) == "" || (aphasiaType != "" && aphasiaType != "motor_aphasia") {
		return out.ProgramInfo{}, corerr.ErrNoProgramForClassifier
	}
	return info, nil
}

func (c *Catalog) GetProgram(ctx context.Context, programID string, version int) (out.ProgramInfo, error) {
	if c.Err != nil {
		return out.ProgramInfo{}, c.Err
	}
	id, err := uuid.Parse(programID)
	if err != nil {
		return out.ProgramInfo{}, corerr.ErrProgramNotFound
	}
	info, ok := c.Programs[id]
	if !ok {
		return out.ProgramInfo{}, corerr.ErrProgramNotFound
	}
	if version > 0 && version != info.Version {
		return out.ProgramInfo{}, corerr.ErrProgramNotFound
	}
	return info, nil
}

// Storage is a non-configured object storage (presign endpoints return 503).
type Storage struct {
	ConfiguredValue bool
	Err             error
}

func (s *Storage) PresignPut(context.Context, string, string, time.Duration) (string, error) {
	if s.Err != nil {
		return "", s.Err
	}
	if !s.ConfiguredValue {
		return "", corerr.ErrS3NotConfigured
	}
	return "https://storage.yandexcloud.net/saymo-answers/presigned-put", nil
}

func (s *Storage) PresignGet(context.Context, string, time.Duration) (string, error) {
	if s.Err != nil {
		return "", s.Err
	}
	return "https://storage.yandexcloud.net/saymo-answers/presigned-get", nil
}

func (s *Storage) Configured() bool { return s.ConfiguredValue }

// Idempotency is an in-memory out.IdempotencyStore.
type Idempotency struct {
	mu    sync.Mutex
	state map[string]out.StoredResponse
}

func NewIdempotency() *Idempotency { return &Idempotency{state: map[string]out.StoredResponse{}} }

func (i *Idempotency) Begin(ctx context.Context, key string, ttl time.Duration) (out.IdempotencyState, *out.StoredResponse, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if resp, ok := i.state[key]; ok {
		cp := resp
		return out.IdempotencyCompleted, &cp, nil
	}
	return out.IdempotencyNew, nil, nil
}

func (i *Idempotency) Complete(ctx context.Context, key string, resp out.StoredResponse, ttl time.Duration) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.state[key] = resp
	return nil
}

func (i *Idempotency) Release(ctx context.Context, key string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.state, key)
	return nil
}

func (i *Idempotency) Ping(context.Context) error { return nil }

// Validator maps raw tokens to auth contexts.
type Validator struct {
	Tokens map[string]in.AuthContext
}

func (v *Validator) ValidateToken(ctx context.Context, raw string) (in.AuthContext, error) {
	if ac, ok := v.Tokens[raw]; ok {
		return ac, nil
	}
	switch raw {
	case "revoked-token":
		return in.AuthContext{}, corerr.ErrTokenRevoked
	default:
		return in.AuthContext{}, corerr.ErrInvalidToken
	}
}

// MustProgram returns the catalog program for a difficulty or panics (test setup only).
func (c *Catalog) MustProgram(difficulty string) out.ProgramInfo {
	info, err := c.GetProgram(context.Background(), ProgramID(difficulty).String(), 0)
	if err != nil {
		panic(errors.New("testkit: unknown program " + difficulty))
	}
	return info
}
