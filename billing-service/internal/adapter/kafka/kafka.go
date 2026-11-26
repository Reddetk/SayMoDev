// Package kafka — outbox publisher и consumer BC#1 для BC#2.
package kafka

// goca:manual — Kafka-адаптеры (outbox/T2) не генерируются goca.

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/Reddetk/SayMoDev/billing-service/internal/platform/metrics"
	"github.com/Reddetk/SayMoDev/billing-service/internal/port/out"
)

// Publisher публикует outbox-записи в saymo.billing.events.
type Publisher struct {
	store    out.Store
	writer   *kafkago.Writer
	interval time.Duration
	logger   *slog.Logger
	metrics  *metrics.Outbox
}

// NewPublisher создаёт publisher.
func NewPublisher(store out.Store, brokers []string, topic string, logger *slog.Logger, outboxMetrics *metrics.Outbox) *Publisher {
	return &Publisher{
		store: store,
		writer: &kafkago.Writer{
			Addr:         kafkago.TCP(brokers...),
			Topic:        topic,
			Balancer:     &kafkago.Hash{},
			RequiredAcks: kafkago.RequireAll,
			BatchTimeout: 100 * time.Millisecond,
		},
		interval: time.Second,
		logger:   logger,
		metrics:  outboxMetrics,
	}
}

// Run опрашивает outbox до отмены ctx.
func (p *Publisher) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = p.writer.Close()
			return
		case <-ticker.C:
			p.publishBatch(ctx)
		}
	}
}

func (p *Publisher) publishBatch(ctx context.Context) {
	records, err := p.store.FetchOutboxBatch(ctx, 100)
	if err != nil {
		p.logger.Error("outbox fetch failed", slog.String("error", err.Error()))
		return
	}
	for _, record := range records {
		message := kafkago.Message{
			Key:   []byte(record.EventID),
			Value: record.Payload,
			Time:  record.OccurredAt,
		}
		if err := p.writer.WriteMessages(ctx, message); err != nil {
			_ = p.store.MarkOutboxFailed(ctx, record.ID, err.Error())
			if p.metrics != nil {
				p.metrics.Failed(record.EventType)
			}
			p.logger.Warn("outbox publish failed", slog.Int64("id", record.ID), slog.String("error", err.Error()))
			continue
		}
		if err := p.store.MarkOutboxSent(ctx, record.ID); err != nil {
			p.logger.Warn("outbox mark sent failed", slog.Int64("id", record.ID), slog.String("error", err.Error()))
		}
		if p.metrics != nil {
			p.metrics.Published(record.EventType)
		}
	}
}

// Consumer читает saymo.identity.events.
type Consumer struct {
	reader  *kafkago.Reader
	service IdentityEventHandler
	logger  *slog.Logger
}

// IdentityEventHandler — application-обработчик событий BC#1.
type IdentityEventHandler interface {
	HandleIdentityEvent(ctx context.Context, event IdentityEvent) error
}

// IdentityEvent — доменное событие BC#1 (Published Language).
type IdentityEvent struct {
	EventID      string          `json:"event_id"`
	EventType    string          `json:"event_type"`
	Version      string          `json:"event_version"`
	Payload      json.RawMessage `json:"payload"`
	EventIDOld   string          `json:"eventId"`
	EventTypeOld string          `json:"eventType"`
}

// Normalize поддерживает оба написания конверта (snake_case — контракт, camelCase — legacy).
func (e *IdentityEvent) Normalize() {
	if e.EventID == "" {
		e.EventID = e.EventIDOld
	}
	if e.EventType == "" {
		e.EventType = e.EventTypeOld
	}
}

// NewConsumer создаёт consumer.
func NewConsumer(brokers []string, topic, group string, service IdentityEventHandler, logger *slog.Logger) *Consumer {
	return &Consumer{
		reader: kafkago.NewReader(kafkago.ReaderConfig{
			Brokers:        brokers,
			Topic:          topic,
			GroupID:        group,
			MinBytes:       1,
			MaxBytes:       10 << 20,
			CommitInterval: 0,
		}),
		service: service,
		logger:  logger,
	}
}

// Run читает сообщения до отмены ctx. Ошибка обработки не коммитит offset.
func (c *Consumer) Run(ctx context.Context) {
	defer func() { _ = c.reader.Close() }()
	for {
		message, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			c.logger.Error("kafka fetch failed", slog.String("error", err.Error()))
			continue
		}
		var event IdentityEvent
		if err := json.Unmarshal(message.Value, &event); err != nil {
			c.logger.Error("invalid identity event", slog.String("error", err.Error()))
			_ = c.reader.CommitMessages(ctx, message)
			continue
		}
		event.Normalize()
		if event.EventID == "" {
			c.logger.Error("identity event without eventId")
			_ = c.reader.CommitMessages(ctx, message)
			continue
		}
		if err := c.service.HandleIdentityEvent(ctx, event); err != nil {
			c.logger.Error("identity event failed",
				slog.String("event_id", event.EventID),
				slog.String("event_type", event.EventType),
				slog.String("error", err.Error()))
			time.Sleep(2 * time.Second)
			continue
		}
		if err := c.reader.CommitMessages(ctx, message); err != nil {
			c.logger.Error("kafka commit failed", slog.String("error", err.Error()))
		}
	}
}
