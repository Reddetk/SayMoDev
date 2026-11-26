// Package usecase — сценарии чтения каталога BC#3.
// goca:manual — L1/L2/singleflight и presigned URL обязательны по docs, генератор их не создаёт.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/domain"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/platform/cache"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/platform/metrics"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/port/out"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

const (
	// ADR-001: read-path BC#3 — cache-first с eventual consistency ≤ 5 min.
	// L1 короче, чтобы потерянное pub/sub-сообщение не растягивало staleness до L2 TTL.
	l1TTL            = time.Minute
	l2TTL            = 5 * time.Minute
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
	repo        out.ProgramRepository
	presigner   out.Presigner
	l1          *cache.Cache
	l2          out.Cache
	invalidator out.CacheInvalidationPublisher
	log         *zap.Logger
	met         *metrics.Metrics
	group       singleflight.Group
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

// SetMetrics включает program_query_latency_seconds и singleflight_* (Observability.md BC#3).
func (q *ProgramQuery) SetMetrics(m *metrics.Metrics) { q.met = m }

// SetInvalidationPublisher включает кросс-репличную инвалидацию L1 (ADR-011).
func (q *ProgramQuery) SetInvalidationPublisher(p out.CacheInvalidationPublisher) {
	q.invalidator = p
}

func (q *ProgramQuery) List(ctx context.Context, filter out.ProgramFilter) ([]out.ProgramSummary, error) {
	if err := validateFilter(filter); err != nil {
		return nil, err
	}

	started := time.Now()
	cacheHit := true
	sharedCall := false
	defer func() {
		q.observeQuery("list", started, cacheHit, sharedCall)
	}()

	// Полный каталог кэшируется под документированным ключом programs:all;
	// фильтры применяются в памяти, чтобы публичный query не плодил ключи L2/БД.
	const key = "programs:all"
	var items []out.ProgramSummary
	if value, ok := q.l1.Get(key); ok {
		items = value.([]out.ProgramSummary)
	} else {
		cacheHit = false
		groupStarted := time.Now()
		value, err, shared := q.group.Do(key, func() (any, error) {
			var cached []out.ProgramSummary
			if q.l2Get(ctx, key, &cached) {
				cacheHit = true
				q.countCacheMiss("program_list")
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
		q.observeSingleflight("list", groupStarted, shared)
		sharedCall = shared
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

	started := time.Now()
	cacheHit := true
	sharedCall := false
	defer func() {
		q.observeQuery("get", started, cacheHit, sharedCall)
	}()

	if value, ok := q.l1.Get(key); ok {
		return value.(out.ProgramRecord), nil
	}
	cacheHit = false

	groupStarted := time.Now()
	value, err, shared := q.group.Do(key, func() (any, error) {
		var cached out.ProgramRecord
		if q.l2Get(ctx, key, &cached) {
			cacheHit = true
			q.countCacheMiss("program")
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
	q.observeSingleflight("get", groupStarted, shared)
	sharedCall = shared
	if err != nil {
		return out.ProgramRecord{}, err
	}
	return value.(out.ProgramRecord), nil
}

func (q *ProgramQuery) GetLesson(ctx context.Context, programID, lessonID string) (out.LessonRecord, error) {
	metaKey := fmt.Sprintf("lesson:%s:%s", programID, lessonID)

	started := time.Now()
	cacheHit := true
	sharedCall := false
	defer func() {
		q.observeQuery("get_lesson", started, cacheHit, sharedCall)
	}()

	var lesson out.LessonRecord
	if value, ok := q.l1.Get(metaKey); ok {
		lesson = value.(out.LessonRecord)
	} else {
		cacheHit = false
		groupStarted := time.Now()
		value, err, shared := q.group.Do(metaKey, func() (any, error) {
			var cached out.LessonRecord
			if q.l2Get(ctx, metaKey, &cached) {
				cacheHit = true
				q.countCacheMiss("lesson")
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
		q.observeSingleflight("get_lesson", groupStarted, shared)
		sharedCall = shared
		if err != nil {
			return out.LessonRecord{}, err
		}
		lesson = value.(out.LessonRecord)
	}

	// Presigned URL живёт 1h; кэш 55min. Ключ включает contentHash: при смене
	// PDF старая ссылка не отдаётся, даже если инвалидация L2 не удалась.
	presignKey := fmt.Sprintf("presigned_url:%s:%s:%s", programID, lessonID, lesson.ContentHash)
	var presigned string
	presignStarted := time.Now()
	value, err, presignShared := q.group.Do("presign:"+presignKey, func() (any, error) {
		var cached presignedRecord
		if q.l2Get(ctx, presignKey, &cached) && time.Until(cached.ExpiresAt) > presignMinRemain {
			// Ссылка с остатком ≤5 мин не отдаётся: клиент получил бы почти истёкший URL.
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
	q.observeSingleflight("presign", presignStarted, presignShared)
	if err != nil {
		return out.LessonRecord{}, err
	}
	presigned = value.(string)

	lesson.ContentURL = presigned
	return lesson, nil
}

func (q *ProgramQuery) observeQuery(endpoint string, started time.Time, cacheHit, shared bool) {
	if q.met == nil {
		return
	}
	q.met.ProgramQueryLatency.WithLabelValues(strconv.FormatBool(cacheHit), strconv.FormatBool(shared)).
		Observe(time.Since(started).Seconds())
	if cacheHit && !shared {
		q.met.SingleflightCalls.WithLabelValues(endpoint, "hit").Inc()
	}
}

func (q *ProgramQuery) observeSingleflight(endpoint string, started time.Time, shared bool) {
	if q.met == nil {
		return
	}
	q.met.SingleflightWait.WithLabelValues(endpoint).Observe(time.Since(started).Seconds())
	if shared {
		q.met.SingleflightCalls.WithLabelValues(endpoint, "shared").Inc()
	} else {
		q.met.SingleflightCalls.WithLabelValues(endpoint, "miss").Inc()
	}
}

func (q *ProgramQuery) countCacheMiss(keyType string) {
	if q.met != nil {
		q.met.CacheMissRecovery.WithLabelValues(keyType).Inc()
	}
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
		if q.met != nil {
			q.met.RedisWriteFailures.WithLabelValues("set").Inc()
		}
	}
}

// InvalidateAll сбрасывает L1 и общий каталог L2 после write.
func (q *ProgramQuery) InvalidateAll(ctx context.Context) error {
	q.l1.Clear()
	var err error
	if q.l2 != nil {
		if deleteErr := q.l2.Delete(ctx, "programs:all"); deleteErr != nil {
			q.log.Warn("invalidate programs:all failed", zap.Error(deleteErr))
			if q.met != nil {
				q.met.RedisWriteFailures.WithLabelValues("delete").Inc()
			}
			err = deleteErr
		}
	}
	q.publishInvalidation(ctx, out.InvalidationScopeAll)
	return err
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
	q.l1.DeletePrefix("programs:all")
	if q.l2 == nil {
		q.publishInvalidation(ctx, out.InvalidationScopeProgram+programID)
		return nil
	}
	var firstErr error
	for _, prefix := range prefixes {
		if err := q.l2.DeletePrefix(ctx, prefix); err != nil {
			q.log.Warn("invalidate prefix failed", zap.String("prefix", prefix), zap.Error(err))
			if q.met != nil {
				q.met.RedisWriteFailures.WithLabelValues("delete_prefix").Inc()
			}
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if err := q.l2.Delete(ctx, "programs:all"); err != nil && firstErr == nil {
		q.log.Warn("invalidate programs:all failed", zap.Error(err))
		if q.met != nil {
			q.met.RedisWriteFailures.WithLabelValues("delete").Inc()
		}
		firstErr = err
	}
	q.publishInvalidation(ctx, out.InvalidationScopeProgram+programID)
	return firstErr
}

// ApplyInvalidation применяет сообщение pub/sub: чистит только локальный L1.
// L2 уже инвалидирован писателем; удалять его здесь нельзя — можно снести более свежее значение.
func (q *ProgramQuery) ApplyInvalidation(scope string) {
	switch {
	case scope == out.InvalidationScopeAll:
		q.l1.Clear()
	case strings.HasPrefix(scope, out.InvalidationScopeProgram):
		programID := strings.TrimPrefix(scope, out.InvalidationScopeProgram)
		if programID == "" {
			return
		}
		for _, prefix := range []string{
			"program:" + programID + ":",
			"lesson:" + programID + ":",
			"presigned_url:" + programID + ":",
		} {
			q.l1.DeletePrefix(prefix)
		}
		q.l1.DeletePrefix("programs:all")
	}
}

func (q *ProgramQuery) publishInvalidation(ctx context.Context, scope string) {
	if q.invalidator == nil {
		return
	}
	if err := q.invalidator.PublishInvalidation(ctx, scope); err != nil {
		q.log.Warn("cache invalidation publish failed", zap.String("scope", scope), zap.Error(err))
		if q.met != nil {
			q.met.RedisWriteFailures.WithLabelValues("publish").Inc()
		}
	}
}
