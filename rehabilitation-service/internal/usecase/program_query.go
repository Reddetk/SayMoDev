// Package usecase — сценарии чтения каталога BC#3.
// goca:manual — L1/L2/singleflight и presigned URL обязательны по docs, генератор их не создаёт.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/domain"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/platform/cache"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/port/out"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

const (
	l1TTL            = 5 * time.Minute
	l2TTL            = time.Hour
	presignTTL       = time.Hour
	presignCacheTTL  = 55 * time.Minute
	presignMinRemain = 5 * time.Minute
)

type presignedRecord struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
}

var ErrInvalidFilter = errors.New("invalid filter")

type ProgramQuery struct {
	repo      out.ProgramRepository
	presigner out.Presigner
	l1        *cache.Cache
	l2        out.Cache
	log       *zap.Logger
	group     singleflight.Group
}

func NewProgramQuery(repo out.ProgramRepository, presigner out.Presigner, l2 out.Cache, log *zap.Logger) *ProgramQuery {
	if log == nil {
		log = zap.NewNop()
	}
	return &ProgramQuery{
		repo:      repo,
		presigner: presigner,
		l1:        cache.New(),
		l2:        l2,
		log:       log,
	}
}

func (q *ProgramQuery) List(ctx context.Context, filter out.ProgramFilter) ([]out.ProgramSummary, error) {
	if err := validateFilter(filter); err != nil {
		return nil, err
	}

	// Полный каталог кэшируется под документированным ключом programs:all;
	// фильтры применяются в памяти, чтобы публичный query не плодил ключи L2/БД.
	const key = "programs:all"
	var items []out.ProgramSummary
	if value, ok := q.l1.Get(key); ok {
		items = value.([]out.ProgramSummary)
	} else {
		value, err, _ := q.group.Do(key, func() (any, error) {
			var cached []out.ProgramSummary
			if q.l2Get(ctx, key, &cached) {
				q.l1.Set(key, cached, l1TTL)
				return cached, nil
			}

			fetched, err := q.repo.List(ctx, out.ProgramFilter{})
			if err != nil {
				return nil, err
			}
			q.l1.Set(key, fetched, l1TTL)
			q.l2Set(ctx, key, fetched, l2TTL)
			return fetched, nil
		})
		if err != nil {
			return nil, err
		}
		items = value.([]out.ProgramSummary)
	}

	filtered := make([]out.ProgramSummary, 0, len(items))
	for _, item := range items {
		if filter.Difficulty != "" && item.Difficulty != filter.Difficulty {
			continue
		}
		if filter.AphasiaType != "" && item.AphasiaType != filter.AphasiaType {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered, nil
}

func (q *ProgramQuery) Get(ctx context.Context, programID string, version int) (out.ProgramRecord, error) {
	key := fmt.Sprintf("program:%s:v:%d", programID, version)
	if value, ok := q.l1.Get(key); ok {
		return value.(out.ProgramRecord), nil
	}

	value, err, _ := q.group.Do(key, func() (any, error) {
		var cached out.ProgramRecord
		if q.l2Get(ctx, key, &cached) {
			q.l1.Set(key, cached, l1TTL)
			return cached, nil
		}

		record, err := q.repo.Get(ctx, programID, version)
		if err != nil {
			return nil, err
		}
		q.l1.Set(key, record, l1TTL)
		q.l2Set(ctx, key, record, l2TTL)
		return record, nil
	})
	if err != nil {
		return out.ProgramRecord{}, err
	}
	return value.(out.ProgramRecord), nil
}

func (q *ProgramQuery) GetLesson(ctx context.Context, programID, lessonID string) (out.LessonRecord, error) {
	metaKey := fmt.Sprintf("lesson:%s:%s", programID, lessonID)
	var lesson out.LessonRecord
	if value, ok := q.l1.Get(metaKey); ok {
		lesson = value.(out.LessonRecord)
	} else {
		value, err, _ := q.group.Do(metaKey, func() (any, error) {
			var cached out.LessonRecord
			if q.l2Get(ctx, metaKey, &cached) {
				q.l1.Set(metaKey, cached, l1TTL)
				return cached, nil
			}
			record, err := q.repo.GetLesson(ctx, programID, lessonID)
			if err != nil {
				return nil, err
			}
			q.l1.Set(metaKey, record, l1TTL)
			q.l2Set(ctx, metaKey, record, l2TTL)
			return record, nil
		})
		if err != nil {
			return out.LessonRecord{}, err
		}
		lesson = value.(out.LessonRecord)
	}

	// Presigned URL живёт 1h; кэш 55min. Ссылка с остатком <=5min не отдаётся:
	// перегенерируем, иначе клиент получит почти истёкший URL.
	presignKey := fmt.Sprintf("presigned_url:%s:%s", programID, lessonID)
	var presigned string
	value, err, _ := q.group.Do("presign:"+presignKey, func() (any, error) {
		var cached presignedRecord
		if q.l2Get(ctx, presignKey, &cached) && time.Until(cached.ExpiresAt) > presignMinRemain {
			return cached.URL, nil
		}

		if q.presigner == nil {
			return nil, fmt.Errorf("presigner is not configured")
		}
		generatedAt := time.Now()
		generated, err := q.presigner.PresignGet(ctx, lesson.ContentURL, presignTTL)
		if err != nil {
			return nil, err
		}
		q.l2Set(ctx, presignKey, presignedRecord{URL: generated, ExpiresAt: generatedAt.Add(presignTTL)}, presignCacheTTL)
		return generated, nil
	})
	if err != nil {
		return out.LessonRecord{}, err
	}
	presigned = value.(string)

	lesson.ContentURL = presigned
	return lesson, nil
}

func validateFilter(filter out.ProgramFilter) error {
	if filter.Difficulty != "" {
		switch filter.Difficulty {
		case "easy", "medium", "hard":
		default:
			return ErrInvalidFilter
		}
	}
	if filter.AphasiaType != "" {
		// Та же структурная валидация, что и домен: ≤50 рун, без обрамляющих пробелов.
		if _, err := domain.NewClassifier("easy", filter.AphasiaType); err != nil {
			return ErrInvalidFilter
		}
	}
	return nil
}

func (q *ProgramQuery) l2Get(ctx context.Context, key string, dst any) bool {
	if q.l2 == nil {
		return false
	}
	hit, err := q.l2.Get(ctx, key, dst)
	if err != nil {
		q.log.Warn("l2 cache get failed", zap.String("key", key), zap.Error(err))
		return false
	}
	return hit
}

func (q *ProgramQuery) l2Set(ctx context.Context, key string, value any, ttl time.Duration) {
	if q.l2 == nil {
		return
	}
	if err := q.l2.Set(ctx, key, value, ttl); err != nil {
		q.log.Warn("l2 cache set failed", zap.String("key", key), zap.Error(err))
	}
}

// InvalidateAll сбрасывает L1 и общий каталог L2 после write.
func (q *ProgramQuery) InvalidateAll(ctx context.Context) error {
	q.l1.Clear()
	if q.l2 == nil {
		return nil
	}
	if err := q.l2.Delete(ctx, "programs:all"); err != nil {
		q.log.Warn("invalidate programs:all failed", zap.Error(err))
		return err
	}
	return nil
}

// InvalidateProgram удаляет кэш программы, её уроков и presigned URL.
func (q *ProgramQuery) InvalidateProgram(ctx context.Context, programID string) error {
	prefixes := []string{
		"program:" + programID + ":",
		"lesson:" + programID + ":",
		"presigned_url:" + programID + ":",
	}
	for _, prefix := range prefixes {
		q.l1.DeletePrefix(prefix)
	}
	if q.l2 == nil {
		return nil
	}
	var firstErr error
	for _, prefix := range prefixes {
		if err := q.l2.DeletePrefix(ctx, prefix); err != nil {
			q.log.Warn("invalidate prefix failed", zap.String("prefix", prefix), zap.Error(err))
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if err := q.l2.Delete(ctx, "programs:all"); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}
