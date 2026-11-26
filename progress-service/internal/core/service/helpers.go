// Package service implements BC#4 use cases on top of domain entities and secondary ports.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/Reddetk/SayMoDev/progress-service/internal/core/entity"
	"github.com/Reddetk/SayMoDev/progress-service/internal/core/valobj"
	"github.com/Reddetk/SayMoDev/progress-service/internal/logger"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

var tracer = otel.Tracer("progress-service")

const (
	cacheKeyProgress      = "progress:%s"
	cacheKeyAccessible    = "accessible_lessons:%s"
	cacheKeyActiveProgram = "active_program:%s"
	cacheKeyLessonDone    = "lesson_completed:%s:%s"
	cacheTTLLessonDone    = 30 * 24 * time.Hour
	defaultTimezone       = "UTC"
)

func progressKey(accountID uuid.UUID) string   { return fmt.Sprintf(cacheKeyProgress, accountID) }
func accessibleKey(accountID uuid.UUID) string { return fmt.Sprintf(cacheKeyAccessible, accountID) }
func activeProgramKey(accountID uuid.UUID) string {
	return fmt.Sprintf(cacheKeyActiveProgram, accountID)
}
func lessonDoneKey(accountID, lessonID uuid.UUID) string {
	return fmt.Sprintf(cacheKeyLessonDone, accountID, lessonID)
}

// toProgressEntity maps a port record into the domain aggregate.
func toProgressEntity(rec out.ProgressRecord) entity.PatientProgress {
	p := entity.PatientProgress{
		AccountID:         rec.AccountID,
		ProgramID:         rec.ProgramID,
		ProgramVersion:    rec.ProgramVersion,
		Status:            valobj.ProgramStatus(rec.Status),
		CompletedLessons:  rec.CompletedLessons,
		TotalLessons:      rec.TotalLessons,
		CompletionPercent: valobj.CompletionPercent(rec.CompletionPercent),
		LastCompletedAt:   rec.LastCompletedAt,
		LastUpdatedAt:     rec.LastUpdatedAt,
	}
	if rec.Tariff != nil {
		p.Tariff = &valobj.TariffSnapshot{
			WeekBitmap:  rec.Tariff.WeekBitmap,
			Timezone:    rec.Tariff.Timezone,
			ValidUntil:  rec.Tariff.ValidUntil,
			ActivatedAt: rec.Tariff.ActivatedAt,
		}
	}
	return p
}

// toProgressRecord maps the aggregate back to the persistence DTO.
func toProgressRecord(p entity.PatientProgress) out.ProgressRecord {
	rec := out.ProgressRecord{
		AccountID:         p.AccountID,
		ProgramID:         p.ProgramID,
		ProgramVersion:    p.ProgramVersion,
		Status:            string(p.Status),
		CompletedLessons:  p.CompletedLessons,
		TotalLessons:      p.TotalLessons,
		CompletionPercent: int(p.CompletionPercent),
		LastCompletedAt:   p.LastCompletedAt,
		LastUpdatedAt:     p.LastUpdatedAt,
	}
	if p.Tariff != nil {
		rec.Tariff = &out.TariffSnapshotRecord{
			WeekBitmap:  p.Tariff.WeekBitmap,
			Timezone:    p.Tariff.Timezone,
			ValidUntil:  p.Tariff.ValidUntil,
			ActivatedAt: p.Tariff.ActivatedAt,
		}
	}
	return rec
}

func defaultTariff(now time.Time, weekBitmap int) *valobj.TariffSnapshot {
	if weekBitmap <= 0 || weekBitmap > 127 {
		return nil
	}
	return &valobj.TariffSnapshot{
		WeekBitmap:  weekBitmap,
		Timezone:    defaultTimezone,
		ActivatedAt: now.UTC(),
	}
}

func newOutbox(ctx context.Context, topicEventType string, payload map[string]any, now time.Time) (out.OutboxEvent, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return out.OutboxEvent{}, fmt.Errorf("marshal event %s: %w", topicEventType, err)
	}
	correlationID := ""
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		correlationID = sc.TraceID().String()
	}
	if correlationID == "" {
		correlationID = uuid.NewString()
	}
	return out.OutboxEvent{
		ID:            uuid.New(),
		EventID:       uuid.NewString(),
		EventType:     topicEventType,
		EventVersion:  1,
		CorrelationID: correlationID,
		Payload:       body,
		OccurredAt:    now.UTC(),
	}, nil
}

// cacheRec is a small helper that never fails a use case because of cache issues.
type cacheRec struct {
	cache    out.Cache
	log      logger.Logger
	localTTL time.Duration
}

func (c cacheRec) get(ctx context.Context, key string, dst any) bool {
	if c.cache == nil {
		return false
	}
	raw, ok, err := c.cache.Get(ctx, key)
	if err != nil {
		c.log.Warn("cache_get_failed", zap.String("key", key), zap.Error(err))
		return false
	}
	if !ok {
		return false
	}
	if err := json.Unmarshal([]byte(raw), dst); err != nil {
		c.log.Warn("cache_decode_failed", zap.String("key", key), zap.Error(err))
		return false
	}
	return true
}

func (c cacheRec) set(ctx context.Context, key string, val any) {
	if c.cache == nil {
		return
	}
	raw, err := json.Marshal(val)
	if err != nil {
		return
	}
	if err := c.cache.Set(ctx, key, string(raw), c.localTTL); err != nil {
		c.log.Warn("cache_set_failed", zap.String("key", key), zap.Error(err))
	}
}

func (c cacheRec) del(ctx context.Context, keys ...string) {
	if c.cache == nil || len(keys) == 0 {
		return
	}
	if err := c.cache.Del(ctx, keys...); err != nil {
		c.log.Warn("cache_del_failed", zap.Strings("keys", keys), zap.Error(err))
	}
}

func (c cacheRec) setLessonCompleted(ctx context.Context, accountID, lessonID uuid.UUID) {
	if c.cache == nil {
		return
	}
	if err := c.cache.Set(ctx, lessonDoneKey(accountID, lessonID), "1", cacheTTLLessonDone); err != nil {
		c.log.Warn("cache_set_failed", zap.String("key", lessonDoneKey(accountID, lessonID)), zap.Error(err))
	}
}

// pickCurrentProgress selects the row that represents the patient's current program:
// active wins, then completed, then the most recently updated paused row.
func pickCurrentProgress(rows []out.ProgressRecord) *out.ProgressRecord {
	if len(rows) == 0 {
		return nil
	}
	best := 0
	rank := func(s string) int {
		switch s {
		case string(valobj.ProgramStatusActive):
			return 0
		case string(valobj.ProgramStatusCompleted):
			return 1
		default:
			return 2
		}
	}
	for i := range rows {
		if rank(rows[i].Status) < rank(rows[best].Status) {
			best = i
			continue
		}
		if rank(rows[i].Status) == rank(rows[best].Status) && rows[i].LastUpdatedAt.After(rows[best].LastUpdatedAt) {
			best = i
		}
	}
	return &rows[best]
}
