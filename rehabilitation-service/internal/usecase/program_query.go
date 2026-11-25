// Package usecase — сценарии чтения каталога BC#3.
// goca:manual — L1/L2/singleflight и presigned URL обязательны по docs, генератор их не создаёт.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	rediscache "github.com/Reddetk/SayMoDev/rehabilitation-service/internal/adapter/redis"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/platform/cache"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/port/out"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

const (
	l1TTL           = 5 * time.Minute
	l2TTL           = time.Hour
	presignTTL      = time.Hour
	presignCacheTTL = 55 * time.Minute
)

var ErrInvalidFilter = errors.New("invalid filter")

type ProgramQuery struct {
	repo      out.ProgramRepository
	presigner out.Presigner
	l1        *cache.Cache
	l2        *rediscache.Cache
	log       *zap.Logger
	group     singleflight.Group
}

func NewProgramQuery(repo out.ProgramRepository, presigner out.Presigner, l2 *rediscache.Cache, log *zap.Logger) *ProgramQuery {
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
	if filter.Difficulty != "" {
		switch filter.Difficulty {
		case "easy", "medium", "hard":
		default:
			return nil, ErrInvalidFilter
		}
	}
	if filter.AphasiaType != "" && filter.AphasiaType != trimSpace(filter.AphasiaType) {
		return nil, ErrInvalidFilter
	}

	key := fmt.Sprintf("programs:all:%s:%s", filter.Difficulty, filter.AphasiaType)
	if value, ok := q.l1.Get(key); ok {
		return value.([]out.ProgramSummary), nil
	}

	value, err, _ := q.group.Do(key, func() (any, error) {
		var cached []out.ProgramSummary
		if q.l2Get(ctx, key, &cached) {
			return cached, nil
		}

		items, err := q.repo.List(ctx, filter)
		if err != nil {
			return nil, err
		}
		q.l1.Set(key, items, l1TTL)
		q.l2Set(ctx, key, items, l2TTL)
		return items, nil
	})
	if err != nil {
		return nil, err
	}
	return value.([]out.ProgramSummary), nil
}

func (q *ProgramQuery) Get(ctx context.Context, programID string, version int) (out.ProgramRecord, error) {
	key := fmt.Sprintf("program:%s:v:%d", programID, version)
	if value, ok := q.l1.Get(key); ok {
		return value.(out.ProgramRecord), nil
	}

	value, err, _ := q.group.Do(key, func() (any, error) {
		var cached out.ProgramRecord
		if q.l2Get(ctx, key, &cached) {
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
	lesson, err := q.repo.GetLesson(ctx, programID, lessonID)
	if err != nil {
		return out.LessonRecord{}, err
	}

	presignKey := "presigned_url:" + lessonID
	var presigned string
	if q.l2Get(ctx, presignKey, &presigned) && presigned != "" {
		lesson.ContentURL = presigned
		return lesson, nil
	}

	if q.presigner == nil {
		return out.LessonRecord{}, fmt.Errorf("presigner is not configured")
	}
	presigned, err = q.presigner.PresignGet(ctx, lesson.ContentURL, presignTTL)
	if err != nil {
		return out.LessonRecord{}, err
	}
	q.l2Set(ctx, presignKey, presigned, presignCacheTTL)
	lesson.ContentURL = presigned
	return lesson, nil
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

func trimSpace(value string) string {
	start, end := 0, len(value)
	for start < end && (value[start] == ' ' || value[start] == '\t' || value[start] == '\n' || value[start] == '\r') {
		start++
	}
	for end > start && (value[end-1] == ' ' || value[end-1] == '\t' || value[end-1] == '\n' || value[end-1] == '\r') {
		end--
	}
	return value[start:end]
}
