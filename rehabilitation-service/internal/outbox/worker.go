// goca:manual — outbox publisher BC#3 (docs EventDataFlow T1/T2).
package outbox

import (
	"context"
	"encoding/json"
	"time"

	"go.uber.org/zap"
)

type Message struct {
	ID          string
	EventType   string
	AggregateID string
	Payload     []byte
	CreatedAt   time.Time
}

type Store interface {
	FetchUnsent(ctx context.Context, limit int) ([]Message, error)
	MarkSent(ctx context.Context, ids []string) error
}

type Publisher interface {
	Publish(ctx context.Context, topic, key string, value []byte) error
}

type Worker struct {
	store     Store
	publisher Publisher
	topic     string
	batch     int
	interval  time.Duration
	log       *zap.Logger
}

func NewWorker(store Store, publisher Publisher, topic string, log *zap.Logger) *Worker {
	if log == nil {
		log = zap.NewNop()
	}
	return &Worker{store: store, publisher: publisher, topic: topic, batch: 100, interval: time.Second, log: log}
}

// Run опрашивает outbox до отмены ctx.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := w.Drain(ctx); err != nil {
				w.log.Warn("outbox drain failed", zap.Error(err))
			}
		}
	}
}

// Drain публикует одну пачку и помечает sent только после успешной отправки.
func (w *Worker) Drain(ctx context.Context) (int, error) {
	messages, err := w.store.FetchUnsent(ctx, w.batch)
	if err != nil {
		return 0, err
	}
	if len(messages) == 0 {
		return 0, nil
	}

	sent := make([]string, 0, len(messages))
	for _, message := range messages {
		envelope, err := json.Marshal(map[string]any{
			"event_id":      message.ID,
			"event_type":    message.EventType,
			"event_version": 1,
			"occurred_at":   message.CreatedAt.UTC().Format(time.RFC3339),
			"payload":       json.RawMessage(message.Payload),
		})
		if err != nil {
			return len(sent), err
		}
		if err := w.publisher.Publish(ctx, w.topic, message.AggregateID, envelope); err != nil {
			if len(sent) > 0 {
				_ = w.store.MarkSent(ctx, sent)
			}
			return len(sent), err
		}
		sent = append(sent, message.ID)
	}
	if err := w.store.MarkSent(ctx, sent); err != nil {
		return 0, err
	}
	w.log.Info("outbox batch sent", zap.Int("count", len(sent)))
	return len(sent), nil
}
