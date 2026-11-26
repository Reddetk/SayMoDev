// goca:manual — consumers BC#4 events с идемпотентностью (docs T2, throttle 50/s).
package consumer

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/platform/metrics"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type Message struct {
	Topic     string
	Partition int
	Offset    int64
	Key       []byte
	Value     []byte
}

type Reader interface {
	ReadMessage(ctx context.Context) (Message, error)
	Commit(ctx context.Context, message Message) error
	Close() error
}

// lagReader опционален: адаптер может отдавать отставание консьюмера.
type lagReader interface {
	Lag() int64
}

// referenceTotals опционален: позволяет держать active_reference_count_total в актуальном виде.
type referenceTotals interface {
	ActiveReferenceTotal(ctx context.Context) (int, error)
}

// ProcessedStore применяет событие идемпотентно, внутри транзакции.
type ProcessedStore interface {
	ApplyProgramEvent(ctx context.Context, eventID, eventType, programID string, delta int) (applied bool, err error)
}

type Worker struct {
	reader Reader
	store  ProcessedStore
	log    *zap.Logger
	met    *metrics.Metrics
}

func NewWorker(reader Reader, store ProcessedStore, log *zap.Logger) *Worker {
	if log == nil {
		log = zap.NewNop()
	}
	return &Worker{reader: reader, store: store, log: log}
}

// SetMetrics включает event_processing_lag_seconds, kafka_consumer_* (Observability.md BC#3).
func (w *Worker) SetMetrics(m *metrics.Metrics) { w.met = m }

type event struct {
	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	ProgramID  string `json:"program_id"`
	ProgramRef string `json:"programId"`
	OccurredAt string `json:"occurred_at"`
	Payload    struct {
		ProgramID string `json:"programId"`
	} `json:"payload"`
}

func (e event) programID() string {
	switch {
	case e.ProgramID != "":
		return e.ProgramID
	case e.ProgramRef != "":
		return e.ProgramRef
	default:
		return e.Payload.ProgramID
	}
}

// Run читает ProgramActivated/ProgramCompleted, throttle — 50 events/sec.
func (w *Worker) Run(ctx context.Context) {
	defer w.reader.Close()
	throttle := time.NewTicker(20 * time.Millisecond)
	defer throttle.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		message, err := w.reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			w.log.Warn("kafka read failed", zap.Error(err))
			continue
		}

		throttleStarted := time.Now()
		<-throttle.C
		if elapsed := time.Since(throttleStarted); elapsed >= 5*time.Millisecond {
			eventType := eventTypeOf(message.Value)
			if w.met != nil {
				w.met.KafkaConsumerThrottled.WithLabelValues(eventType).Inc()
				w.met.ThrottledDelay.WithLabelValues(eventType).Observe(elapsed.Seconds())
			}
		}

		if err := w.handle(ctx, message); err != nil {
			// Останавливаем партицию, чтобы не коммитить более поздний offset и не потерять событие.
			w.log.Error("event processing failed, stopping consumer", zap.Error(err))
			return
		}
		if err := w.reader.Commit(ctx, message); err != nil {
			w.log.Warn("kafka commit failed", zap.Error(err))
		}
	}
}

func eventTypeOf(raw []byte) string {
	var evt struct {
		EventType string `json:"event_type"`
	}
	if err := json.Unmarshal(raw, &evt); err != nil || evt.EventType == "" {
		return "unknown"
	}
	return evt.EventType
}

func (w *Worker) handle(ctx context.Context, message Message) error {
	var evt event
	if err := json.Unmarshal(message.Value, &evt); err != nil {
		// Невалидное сообщение не блокирует партицию: пропускаем в DLQ-лог.
		w.log.Warn("invalid event payload", zap.Error(err))
		return nil
	}

	if w.met != nil {
		if reader, ok := w.reader.(lagReader); ok {
			w.met.KafkaConsumerQueueLength.WithLabelValues(eventTypeOrUnknown(evt.EventType)).Set(float64(reader.Lag()))
		}
		if evt.OccurredAt != "" {
			if occurred, err := time.Parse(time.RFC3339, evt.OccurredAt); err == nil {
				lag := time.Since(occurred).Seconds()
				if lag >= 0 {
					w.met.EventProcessingLag.WithLabelValues(eventTypeOrUnknown(evt.EventType)).Observe(lag)
				}
			}
		}
	}

	delta := 0
	switch evt.EventType {
	case "ProgramActivated":
		delta = 1
	case "ProgramCompleted":
		delta = -1
	default:
		return nil
	}
	if evt.EventID == "" || evt.programID() == "" {
		w.log.Warn("event without id/program_id", zap.String("type", evt.EventType))
		return nil
	}
	if _, err := uuid.Parse(evt.EventID); err != nil {
		w.log.Warn("event_id is not a UUID", zap.String("event_id", evt.EventID))
		return nil
	}
	if _, err := uuid.Parse(evt.programID()); err != nil {
		w.log.Warn("program_id is not a UUID", zap.String("program_id", evt.programID()))
		return nil
	}

	applied, err := w.store.ApplyProgramEvent(ctx, evt.EventID, evt.EventType, evt.programID(), delta)
	if err != nil {
		return err
	}
	if !applied {
		w.log.Debug("duplicate event skipped", zap.String("event_id", evt.EventID))
		return nil
	}
	if w.met != nil {
		if totals, ok := w.store.(referenceTotals); ok {
			if total, err := totals.ActiveReferenceTotal(ctx); err == nil {
				w.met.ActiveReferenceCount.Set(float64(total))
			}
		}
	}
	return nil
}

func eventTypeOrUnknown(eventType string) string {
	if eventType == "" {
		return "unknown"
	}
	return eventType
}
