package kafka

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

type stubLogger struct{}

func (stubLogger) Debug(string, ...zap.Field) {}
func (stubLogger) Info(string, ...zap.Field)  {}
func (stubLogger) Warn(string, ...zap.Field)  {}
func (stubLogger) Error(string, ...zap.Field) {}
func (stubLogger) Fatal(string, ...zap.Field) {}

type outboxRepo struct {
	mu     sync.Mutex
	rows   []out.OutboxEvent
	marked []uuid.UUID
}

func (r *outboxRepo) FetchUnsentOutbox(context.Context, int) ([]out.OutboxEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rows, nil
}

func (r *outboxRepo) MarkOutboxSent(_ context.Context, ids []uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.marked = append(r.marked, ids...)
	return nil
}

type recordingPublisher struct {
	mu   sync.Mutex
	msgs []struct{ topic, key, payload string }
	err  error
}

func (p *recordingPublisher) Publish(_ context.Context, topic, key string, payload []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.msgs = append(p.msgs, struct{ topic, key, payload string }{topic, key, string(payload)})
	return nil
}

func TestOutboxWorkerPublishesAndMarksSent(t *testing.T) {
	payload, err := json.Marshal(map[string]string{"accountId": "acc-1"})
	require.NoError(t, err)
	row := out.OutboxEvent{ID: uuid.New(), EventID: "evt-1", EventType: "LessonCompleted", Payload: payload, OccurredAt: time.Now()}
	repo := &outboxRepo{rows: []out.OutboxEvent{row}}
	pub := &recordingPublisher{}
	w := NewOutboxWorker(repo, pub, "saymo.progress.events", 10, time.Millisecond, stubLogger{})

	w.process(context.Background())

	require.Len(t, pub.msgs, 1)
	assert.Equal(t, "saymo.progress.events", pub.msgs[0].topic)
	assert.Equal(t, "acc-1", pub.msgs[0].key, "partition key must be accountId")

	var env Envelope
	require.NoError(t, json.Unmarshal([]byte(pub.msgs[0].payload), &env))
	assert.Equal(t, "evt-1", env.EventID)
	assert.Equal(t, "LessonCompleted", env.EventType)
	assert.Equal(t, []uuid.UUID{row.ID}, repo.marked)
}

func TestOutboxWorkerKeepsRowWhenPublishFails(t *testing.T) {
	payload, _ := json.Marshal(map[string]string{"accountId": "acc-1"})
	row := out.OutboxEvent{ID: uuid.New(), EventID: "evt-2", EventType: "LessonCompleted", Payload: payload, OccurredAt: time.Now()}
	repo := &outboxRepo{rows: []out.OutboxEvent{row}}
	pub := &recordingPublisher{err: assert.AnError}
	w := NewOutboxWorker(repo, pub, "t", 10, time.Millisecond, stubLogger{})

	w.process(context.Background())

	assert.Empty(t, repo.marked, "unsent rows must be retried")
}

func TestBuildEnvelope(t *testing.T) {
	raw, err := BuildEnvelope("id-1", "ProgramActivated", time.Now(), []byte(`{"accountId":"a"}`))
	require.NoError(t, err)
	var env Envelope
	require.NoError(t, json.Unmarshal(raw, &env))
	assert.Equal(t, "id-1", env.EventID)
	assert.JSONEq(t, `{"accountId":"a"}`, string(env.Payload))
}
