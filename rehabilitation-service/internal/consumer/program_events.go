// goca:manual — consumers BC#4 events с идемпотентностью (docs T2, throttle 50/s).
package consumer

import (
	"context"
	"encoding/json"
	"time"

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

// ProcessedStore применяет событие идемпотентно, внутри транзакции.
type ProcessedStore interface {
	ApplyProgramEvent(ctx context.Context, eventID, eventType, programID string, delta int) (applied bool, err error)
}

type Worker struct {
	reader Reader
	store  ProcessedStore
	log    *zap.Logger
}

func NewWorker(reader Reader, store ProcessedStore, log *zap.Logger) *Worker {
	if log == nil {
		log = zap.NewNop()
	}
	return &Worker{reader: reader, store: store, log: log}
}

type event struct {
	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	ProgramID  string `json:"program_id"`
	ProgramRef string `json:"programId"`
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
		<-throttle.C

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

func (w *Worker) handle(ctx context.Context, message Message) error {
	var evt event
	if err := json.Unmarshal(message.Value, &evt); err != nil {
		// Невалидное сообщение не блокирует партицию: пропускаем в DLQ-лог.
		w.log.Warn("invalid event payload", zap.Error(err))
		return nil
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
	}
	return nil
}
