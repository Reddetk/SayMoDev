package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"github.com/Reddetk/SayMoDev/progress-service/internal/adapter/telemetry"
	"github.com/Reddetk/SayMoDev/progress-service/internal/logger"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/in"
)

const (
	retryDelay          = 200 * time.Millisecond
	consumerRetryWindow = 5 * time.Minute
	maxRetryBackoff     = 10 * time.Second
)

// Consumer reads BC#1/#2/#3 topics and dispatches to the event sink.
type Consumer struct {
	readers  []*kafka.Reader
	sink     in.EventSink
	dlq      Publisher
	dlqTopic string
	log      logger.Logger
}

// NewConsumer creates one reader per topic in the progress-service consumer group.
func NewConsumer(brokers []string, groupID string, topics []string, sink in.EventSink, dlq Publisher, dlqTopic string, log logger.Logger) *Consumer {
	c := &Consumer{sink: sink, dlq: dlq, dlqTopic: dlqTopic, log: log}
	for _, topic := range topics {
		c.readers = append(c.readers, kafka.NewReader(kafka.ReaderConfig{
			Brokers:        brokers,
			GroupID:        groupID,
			Topic:          topic,
			MinBytes:       1,
			MaxBytes:       10 << 20,
			CommitInterval: 0, // manual commit after the use case succeeds
			StartOffset:    kafka.FirstOffset,
		}))
	}
	return c
}

// Run consumes until ctx is cancelled.
func (c *Consumer) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, r := range c.readers {
		wg.Add(1)
		go func(r *kafka.Reader) {
			defer wg.Done()
			c.consume(ctx, r)
		}(r)
	}
	wg.Wait()
}

// Close closes all readers.
func (c *Consumer) Close() error {
	var errs []error
	for _, r := range c.readers {
		errs = append(errs, r.Close())
	}
	return errors.Join(errs...)
}

func (c *Consumer) consume(ctx context.Context, r *kafka.Reader) {
	for {
		msg, err := r.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			c.log.Warn("kafka_fetch_failed", zap.String("topic", r.Config().Topic), zap.Error(err))
			time.Sleep(retryDelay)
			continue
		}

		if !c.handleMessage(ctx, msg) {
			// No commit: stop this partition so the failed message is redelivered on restart.
			// Continuing would commit the NEXT message and advance the offset past this one.
			return
		}
		if err := r.CommitMessages(ctx, msg); err != nil {
			c.log.Warn("kafka_commit_failed", zap.String("topic", msg.Topic), zap.Error(err))
		}
	}
}

// handleMessage returns true when the message may be committed. A false result means the
// partition must stop: committing any later offset would silently drop this message.
func (c *Consumer) handleMessage(ctx context.Context, msg kafka.Message) bool {
	env, decodeErr := decodeEnvelope(msg)
	if decodeErr != nil {
		return c.deadLetterReliable(ctx, msg, env, decodeErr)
	}
	if err := c.dispatchWithRetry(ctx, env); err != nil {
		if ctx.Err() != nil {
			return false
		}
		// Last resort after the retry window to avoid a permanently stuck partition.
		return c.deadLetterReliable(ctx, msg, env, err)
	}
	return true
}

// deadLetterReliable retries the DLQ write until it is durable or the consumer stops.
func (c *Consumer) deadLetterReliable(ctx context.Context, msg kafka.Message, env Envelope, cause error) bool {
	backoff := retryDelay
	for {
		if c.deadLetter(ctx, msg, env, cause) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(backoff):
		}
		if backoff < maxRetryBackoff {
			backoff *= 2
			if backoff > maxRetryBackoff {
				backoff = maxRetryBackoff
			}
		}
	}
}

// dispatchWithRetry retries transient failures (BC#3/DB outages) without committing the offset.
func (c *Consumer) dispatchWithRetry(ctx context.Context, env Envelope) error {
	deadline := time.Now().Add(consumerRetryWindow)
	backoff := retryDelay
	for {
		err := c.dispatch(ctx, env)
		if err == nil {
			telemetry.EventProcessingLag.WithLabelValues(env.EventType).Observe(eventLag(env))
			if strings.HasPrefix(env.EventType, "Tariff") || strings.HasPrefix(env.EventType, "Subscription") {
				telemetry.AccessRecalculationsTotal.WithLabelValues(env.EventType).Inc()
			}
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < maxRetryBackoff {
			backoff *= 2
			if backoff > maxRetryBackoff {
				backoff = maxRetryBackoff
			}
		}
	}
}

func eventLag(env Envelope) float64 {
	if env.OccurredAt == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, env.OccurredAt)
	if err != nil {
		if t, err = time.Parse(time.RFC3339, env.OccurredAt); err != nil {
			return 0
		}
	}
	lag := time.Since(t).Seconds()
	if lag < 0 {
		return 0
	}
	return lag
}

func (c *Consumer) dispatch(ctx context.Context, env Envelope) error {
	switch env.EventType {
	case "AccountRegistered":
		var p accountRegisteredPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return fmt.Errorf("decode AccountRegistered: %w", err)
		}
		return c.sink.OnAccountRegistered(ctx, in.RegistrationEvent{
			EventID:       env.EventID,
			AccountID:     p.accountID(),
			Role:          p.Role,
			Difficulty:    p.difficulty(),
			AphasiaType:   p.aphasiaType(),
			HasClassifier: p.difficulty() != "",
		})

	case "AccountDeleted":
		var p accountRegisteredPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return fmt.Errorf("decode AccountDeleted: %w", err)
		}
		accountID, err := uuid.Parse(p.accountID())
		if err != nil {
			return fmt.Errorf("decode AccountDeleted accountId: %w", err)
		}
		return c.sink.OnAccountDeleted(ctx, in.AccountDeletedEvent{EventID: env.EventID, AccountID: accountID})

	case "TariffPlanActivated", "TariffPlanChanged", "TariffPlanDeActivated",
		"SubscriptionActivated", "SubscriptionChanged", "SubscriptionSuspended",
		"SubscriptionResumed", "SubscriptionRenewed", "SubscriptionExpired", "SubscriptionDeActivated":
		var p tariffPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return fmt.Errorf("decode %s: %w", env.EventType, err)
		}
		accountID, err := uuid.Parse(p.accountID())
		if err != nil {
			return fmt.Errorf("decode %s accountId: %w", env.EventType, err)
		}
		ev := in.TariffEvent{
			EventID: env.EventID, AccountID: accountID, EventType: env.EventType,
			WeekBitmap: p.weekBitmap(), Timezone: p.timezone(), ValidUntil: p.validUntil(),
			HasSnapshot: p.hasSnapshot(),
		}
		return c.sink.OnTariffChanged(ctx, ev)

	case "MigrationRequested":
		var p migrationPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return fmt.Errorf("decode MigrationRequested: %w", err)
		}
		programID, err := uuid.Parse(p.ProgramID)
		if err != nil {
			return fmt.Errorf("decode MigrationRequested programId: %w", err)
		}
		ids := make([]uuid.UUID, 0, len(p.AccountIDs))
		for _, raw := range p.AccountIDs {
			id, err := uuid.Parse(raw)
			if err != nil {
				return fmt.Errorf("decode MigrationRequested accountId %q: %w", raw, err)
			}
			ids = append(ids, id)
		}
		return c.sink.OnMigrationRequested(ctx, in.MigrationEvent{
			EventID: env.EventID, JobID: p.JobID, ProgramID: programID,
			TargetVersion: p.targetVersion(), AccountIDs: ids,
		})

	case "LessonAddedToProgram":
		var p lessonAddedPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return fmt.Errorf("decode LessonAddedToProgram: %w", err)
		}
		programID, err := uuid.Parse(p.ProgramID)
		if err != nil {
			return fmt.Errorf("decode LessonAddedToProgram programId: %w", err)
		}
		lessonID, err := uuid.Parse(p.lessonID())
		if err != nil {
			return fmt.Errorf("decode LessonAddedToProgram lessonId: %w", err)
		}
		return c.sink.OnLessonAdded(ctx, in.LessonAddedEvent{
			EventID: env.EventID, ProgramID: programID, LessonID: lessonID,
			Position: p.position(), LessonType: p.lessonType(), NewTotalLessons: p.newTotalLessons(),
		})

	default:
		c.log.Debug("kafka_event_ignored", zap.String("event_type", env.EventType))
		return nil
	}
}

// deadLetter publishes a copy to the DLQ and reports whether the copy is durable.
func (c *Consumer) deadLetter(ctx context.Context, msg kafka.Message, env Envelope, cause error) bool {
	c.log.Error("kafka_event_dead_lettered",
		zap.String("topic", msg.Topic), zap.String("event_type", env.EventType),
		zap.String("event_id", env.EventID), zap.Error(cause))
	if c.dlq == nil {
		return true // no DLQ configured: allow the normal commit path
	}
	payload, _ := json.Marshal(map[string]any{
		"original_topic": msg.Topic, "original_offset": msg.Offset,
		"event": env, "error": cause.Error(), "dead_lettered_at": time.Now().UTC().Format(time.RFC3339),
	})
	key := env.EventID
	if key == "" {
		key = uuid.NewString()
	}
	if err := c.dlq.Publish(ctx, c.dlqTopic, key, payload); err != nil {
		c.log.Error("kafka_dlq_publish_failed", zap.Error(err))
		return false
	}
	return true
}

func decodeEnvelope(msg kafka.Message) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(msg.Value, &env); err != nil {
		return Envelope{EventID: fallbackEventID(msg)}, fmt.Errorf("decode envelope: %w", err)
	}
	if env.EventID == "" {
		env.EventID = fallbackEventID(msg)
	}
	if env.EventType == "" {
		return env, fmt.Errorf("envelope has no event_type")
	}
	return env, nil
}

func fallbackEventID(msg kafka.Message) string {
	return msg.Topic + ":" + strconv.Itoa(msg.Partition) + ":" + strconv.FormatInt(msg.Offset, 10)
}

// --- tolerant payload shapes (docs use camelCase, some examples snake_case) ---

type classifierPayload struct {
	Difficulty   string `json:"difficulty"`
	AphasiaType  string `json:"aphasiaType"`
	AphasiaType2 string `json:"aphasia_type"`
}

type accountRegisteredPayload struct {
	AccountID    string             `json:"accountId"`
	AccountID2   string             `json:"account_id"`
	Role         string             `json:"role"`
	Classifier   *classifierPayload `json:"classifier"`
	Difficulty   string             `json:"difficulty"`
	AphasiaType  string             `json:"aphasiaType"`
	AphasiaType2 string             `json:"aphasia_type"`
}

func (p accountRegisteredPayload) accountID() string {
	if p.AccountID != "" {
		return p.AccountID
	}
	return p.AccountID2
}

func (p accountRegisteredPayload) difficulty() string {
	if p.Classifier != nil && p.Classifier.Difficulty != "" {
		return p.Classifier.Difficulty
	}
	return p.Difficulty
}

func (p accountRegisteredPayload) aphasiaType() string {
	if p.Classifier != nil {
		if p.Classifier.AphasiaType != "" {
			return p.Classifier.AphasiaType
		}
		if p.Classifier.AphasiaType2 != "" {
			return p.Classifier.AphasiaType2
		}
	}
	if p.AphasiaType != "" {
		return p.AphasiaType
	}
	return p.AphasiaType2
}

type snapshotPayload struct {
	WeekBitmap  int     `json:"weekBitmap"`
	WeekBitmap2 int     `json:"week_bitmap"`
	Timezone    string  `json:"timezone"`
	ValidUntil  *string `json:"validUntil"`
	ValidUntil2 *string `json:"valid_until"`
}

type tariffPayload struct {
	AccountID      string           `json:"accountId"`
	AccountID2     string           `json:"account_id"`
	TariffSnapshot *snapshotPayload `json:"tariffSnapshot"`
	Snapshot       *snapshotPayload `json:"snapshot"`
	NewSnapshot    *snapshotPayload `json:"newSnapshot"`
	WeekBitmap     int              `json:"weekBitmap"`
	WeekBitmap2    int              `json:"week_bitmap"`
	Timezone       string           `json:"timezone"`
	ValidUntil     *string          `json:"validUntil"`
}

func (p tariffPayload) accountID() string {
	if p.AccountID != "" {
		return p.AccountID
	}
	return p.AccountID2
}

func (p tariffPayload) snapshot() *snapshotPayload {
	if p.TariffSnapshot != nil {
		return p.TariffSnapshot
	}
	if p.NewSnapshot != nil {
		return p.NewSnapshot
	}
	return p.Snapshot
}

func (p tariffPayload) hasSnapshot() bool {
	return p.snapshot() != nil || p.WeekBitmap != 0 || p.WeekBitmap2 != 0
}

func (p tariffPayload) weekBitmap() int {
	if s := p.snapshot(); s != nil {
		if s.WeekBitmap != 0 {
			return s.WeekBitmap
		}
		return s.WeekBitmap2
	}
	if p.WeekBitmap != 0 {
		return p.WeekBitmap
	}
	return p.WeekBitmap2
}

func (p tariffPayload) timezone() string {
	if s := p.snapshot(); s != nil {
		return s.Timezone
	}
	return p.Timezone
}

func (p tariffPayload) validUntil() *time.Time {
	var raw *string
	if s := p.snapshot(); s != nil {
		if s.ValidUntil != nil {
			raw = s.ValidUntil
		} else {
			raw = s.ValidUntil2
		}
	} else {
		raw = p.ValidUntil
	}
	if raw == nil || *raw == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, *raw)
	if err != nil {
		return nil
	}
	return &t
}

type migrationPayload struct {
	JobID          string   `json:"migration_job_id"`
	JobID2         string   `json:"migrationJobId"`
	ProgramID      string   `json:"programId"`
	ProgramID2     string   `json:"program_id"`
	TargetVersion  int      `json:"targetVersion"`
	TargetVersion2 int      `json:"target_version"`
	AccountIDs     []string `json:"accountIds"`
	AccountIDs2    []string `json:"account_ids"`
}

func (p migrationPayload) jobID() string {
	if p.JobID != "" {
		return p.JobID
	}
	return p.JobID2
}

func (p migrationPayload) targetVersion() int {
	if p.TargetVersion != 0 {
		return p.TargetVersion
	}
	return p.TargetVersion2
}

type lessonAddedPayload struct {
	ProgramID     string `json:"programId"`
	ProgramID2    string `json:"program_id"`
	LessonID      string `json:"lessonId"`
	LessonID2     string `json:"lesson_id"`
	Position      int    `json:"position"`
	OrderPosition int    `json:"order_position"`
	LessonType    string `json:"lessonType"`
	LessonType2   string `json:"lesson_type"`
	NewTotal      int    `json:"newTotalLessons"`
	NewTotal2     int    `json:"new_total_lessons"`
}

func (p lessonAddedPayload) lessonID() string {
	if p.LessonID != "" {
		return p.LessonID
	}
	return p.LessonID2
}

func (p lessonAddedPayload) position() int {
	if p.Position != 0 {
		return p.Position
	}
	return p.OrderPosition
}

func (p lessonAddedPayload) lessonType() string {
	if p.LessonType != "" {
		return p.LessonType
	}
	if p.LessonType2 != "" {
		return p.LessonType2
	}
	return "regular"
}

func (p lessonAddedPayload) newTotalLessons() int {
	if p.NewTotal != 0 {
		return p.NewTotal
	}
	return p.NewTotal2
}
