// Package kafka implements the outbox publisher, worker and event consumers.
package kafka

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"github.com/Reddetk/SayMoDev/progress-service/internal/logger"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

// Producer publishes outbox records to Kafka.
type Producer struct {
	writer *kafka.Writer
}

// NewProducer builds a synchronous Kafka writer.
func NewProducer(brokers []string) *Producer {
	return &Producer{writer: &kafka.Writer{
		Addr:         kafka.TCP(brokers...),
		Balancer:     &kafka.Hash{}, // partition key keeps per-account ordering
		RequiredAcks: kafka.RequireAll,
		BatchTimeout: 10 * time.Millisecond,
	}}
}

// Publish writes one message.
func (p *Producer) Publish(ctx context.Context, topic, key string, payload []byte) error {
	return p.writer.WriteMessages(ctx, kafka.Message{Topic: topic, Key: []byte(key), Value: payload})
}

// Close flushes and closes the writer.
func (p *Producer) Close() error { return p.writer.Close() }

// Envelope is the wire format of every domain event (see EventDataFlow.md T2).
type Envelope struct {
	EventID       string          `json:"event_id"`
	EventType     string          `json:"event_type"`
	EventVersion  int             `json:"event_version"`
	CorrelationID string          `json:"correlation_id"`
	OccurredAt    string          `json:"occurred_at"`
	Payload       json.RawMessage `json:"payload"`
	Error         string          `json:"error,omitempty"`
}

// BuildEnvelope wraps an outbox payload for publication.
func BuildEnvelope(eventID, eventType string, occurredAt time.Time, payload []byte) ([]byte, error) {
	return BuildEnvelopeVersioned(eventID, eventType, 1, "", occurredAt, payload)
}

// BuildEnvelopeVersioned includes the mandatory event_version/correlation_id fields.
func BuildEnvelopeVersioned(eventID, eventType string, version int, correlationID string, occurredAt time.Time, payload []byte) ([]byte, error) {
	if eventID == "" {
		eventID = uuid.NewString()
	}
	if version <= 0 {
		version = 1
	}
	if correlationID == "" {
		correlationID = eventID
	}
	env := Envelope{
		EventID: eventID, EventType: eventType, EventVersion: version,
		CorrelationID: correlationID, OccurredAt: occurredAt.UTC().Format(time.RFC3339Nano), Payload: payload,
	}
	return json.Marshal(env)
}

// Publisher is the minimal publisher surface used by the worker and consumers.
type Publisher interface {
	Publish(ctx context.Context, topic, key string, payload []byte) error
}

// OutboxRepository is the narrow persistence port needed by the worker (ISP).
type OutboxRepository interface {
	FetchUnsentOutbox(ctx context.Context, limit int) ([]out.OutboxEvent, error)
	MarkOutboxSent(ctx context.Context, ids []uuid.UUID) error
}

// OutboxWorker polls unsent rows and publishes them at-least-once.
type OutboxWorker struct {
	repo      OutboxRepository
	publisher Publisher
	topic     string
	batch     int
	interval  time.Duration
	log       logger.Logger
}

// NewOutboxWorker builds the polling worker.
func NewOutboxWorker(repo OutboxRepository, publisher Publisher, topic string, batch int, interval time.Duration, log logger.Logger) *OutboxWorker {
	if batch <= 0 {
		batch = 50
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return &OutboxWorker{repo: repo, publisher: publisher, topic: topic, batch: batch, interval: interval, log: log}
}

// Run polls until ctx is cancelled.
func (w *OutboxWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.process(ctx)
		}
	}
}

func (w *OutboxWorker) process(ctx context.Context) {
	rows, err := w.repo.FetchUnsentOutbox(ctx, w.batch)
	if err != nil {
		w.log.Warn("outbox_fetch_failed", zap.Error(err))
		return
	}
	if len(rows) == 0 {
		return
	}
	sent := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		env, err := BuildEnvelopeVersioned(row.EventID, row.EventType, row.EventVersion, row.CorrelationID, row.OccurredAt, row.Payload)
		if err != nil {
			w.log.Warn("outbox_encode_failed", zap.String("event_id", row.EventID), zap.Error(err))
			continue
		}
		if err := w.publisher.Publish(ctx, w.topic, eventPartitionKey(row.Payload), env); err != nil {
			w.log.Warn("outbox_publish_failed", zap.String("event_id", row.EventID), zap.Error(err))
			continue
		}
		sent = append(sent, row.ID)
	}
	if len(sent) > 0 {
		if err := w.repo.MarkOutboxSent(ctx, sent); err != nil {
			w.log.Warn("outbox_mark_sent_failed", zap.Int("count", len(sent)), zap.Error(err))
		}
	}
}

func eventPartitionKey(payload []byte) string {
	var p struct {
		AccountID  string `json:"accountId"`
		AccountID2 string `json:"account_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return uuid.NewString()
	}
	if p.AccountID != "" {
		return p.AccountID
	}
	if p.AccountID2 != "" {
		return p.AccountID2
	}
	return uuid.NewString()
}

var _ Publisher = (*Producer)(nil)
