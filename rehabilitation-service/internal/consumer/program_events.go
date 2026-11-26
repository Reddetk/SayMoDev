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

	// Retry-политика для read/process/commit: offset не коммитится, пока событие
	// не применено, поэтому retry сохраняет и отсутствие потерь, и доступность.
	retryBase time.Duration
	retryMax  time.Duration
}

func NewWorker(reader Reader, store ProcessedStore, log *zap.Logger) *Worker {
	if log == nil {
		log = zap.NewNop()
	}
	return &Worker{
		reader:    reader,
		store:     store,
		log:       log,
		retryBase: time.Second,
		retryMax:  30 * time.Second,
	}
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
// Любая ошибка (read/process/commit) приводит к retry с backoff, offset не
// коммитится: событие не теряется, партиция не «умирает» навсегда (ADR-011).
func (w *Worker) Run(ctx context.Context) {
	defer w.reader.Close()
	throttle := time.NewTicker(20 * time.Millisecond)
	defer throttle.Stop()

	backoff := w.retryBase
	for {
		if ctx.Err() != nil {
			return
		}

		message, err := w.reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			w.log.Warn("kafka read failed, retrying", zap.Error(err), zap.Duration("backoff", backoff))
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff, w.retryMax)
			continue
		}

		throttleStarted := time.Now()
		select {
		case <-ctx.Done():
			return
		case <-throttle.C:
		}
		if elapsed := time.Since(throttleStarted); elapsed >= 5*time.Millisecond {
			eventType := eventTypeOf(message.Value)
			if w.met != nil {
				w.met.KafkaConsumerThrottled.WithLabelValues(eventType).Inc()
				w.met.ThrottledDelay.WithLabelValues(eventType).Observe(elapsed.Seconds())
			}
		}

		// FetchMessage сдвигает позицию читателя до Commit, поэтому повторный
		// ReadMessage вернул бы уже следующее событие и потерял упавшее.
		// Ретраим обработку ТОГО ЖЕ message; идемпотентность — processed_events.
		for {
			err := w.handle(ctx, message)
			if err == nil {
				break
			}
			w.log.Error("event processing failed, retrying same event", zap.Error(err), zap.Duration("backoff", backoff))
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff, w.retryMax)
		}

		if !w.commit(ctx, message, &backoff) {
			return
		}
		backoff = w.retryBase
	}
}

// commit ретраит фиксацию того же offset: следующее сообщение не читается,
// пока фиксация не прошла, поэтому событие не теряется и не применяется дважды.
func (w *Worker) commit(ctx context.Context, message Message, backoff *time.Duration) bool {
	for {
		if err := w.reader.Commit(ctx, message); err == nil {
			return true
		} else if ctx.Err() != nil {
			return false
		} else {
			w.log.Warn("kafka commit failed, retrying", zap.Error(err), zap.Duration("backoff", *backoff))
		}
		if !sleepCtx(ctx, *backoff) {
			return false
		}
		*backoff = nextBackoff(*backoff, w.retryMax)
	}
}

func nextBackoff(current, maximum time.Duration) time.Duration {
	next := current * 2
	if next > maximum {
		return maximum
	}
	return next
}

func sleepCtx(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
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
