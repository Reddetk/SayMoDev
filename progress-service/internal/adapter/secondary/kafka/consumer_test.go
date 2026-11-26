package kafka

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Reddetk/SayMoDev/progress-service/internal/port/in"
)

func msgWith(value string) kafkago.Message {
	return kafkago.Message{Topic: "t", Partition: 0, Offset: 7, Value: []byte(value)}
}

type dispatchSink struct {
	registrations []in.RegistrationEvent
	deletions     []in.AccountDeletedEvent
	tariffs       []in.TariffEvent
}

func (s *dispatchSink) OnAccountRegistered(_ context.Context, ev in.RegistrationEvent) error {
	s.registrations = append(s.registrations, ev)
	return nil
}
func (s *dispatchSink) OnAccountDeleted(_ context.Context, ev in.AccountDeletedEvent) error {
	s.deletions = append(s.deletions, ev)
	return nil
}
func (s *dispatchSink) OnTariffChanged(_ context.Context, ev in.TariffEvent) error {
	s.tariffs = append(s.tariffs, ev)
	return nil
}
func (s *dispatchSink) OnMigrationRequested(context.Context, in.MigrationEvent) error { return nil }
func (s *dispatchSink) OnLessonAdded(context.Context, in.LessonAddedEvent) error      { return nil }

func TestDispatchAccountRegistered(t *testing.T) {
	sink := &dispatchSink{}
	c := &Consumer{sink: sink, log: stubLogger{}}
	payload, err := json.Marshal(map[string]any{
		"account_id": "11111111-1111-1111-1111-111111111111",
		"role":       "patient",
		"classifier": map[string]string{"difficulty": "medium", "aphasia_type": "motor_aphasia"},
	})
	require.NoError(t, err)
	require.NoError(t, c.dispatch(context.Background(), Envelope{EventID: "e1", EventType: "AccountRegistered", Payload: payload}))
	require.Len(t, sink.registrations, 1)
	assert.Equal(t, "medium", sink.registrations[0].Difficulty)
	assert.Equal(t, "motor_aphasia", sink.registrations[0].AphasiaType)
}

func TestDispatchTariffWithSnapshot(t *testing.T) {
	sink := &dispatchSink{}
	c := &Consumer{sink: sink, log: stubLogger{}}
	payload, _ := json.Marshal(map[string]any{
		"accountId": "11111111-1111-1111-1111-111111111111",
		"tariffSnapshot": map[string]any{
			"weekBitmap": 2, "timezone": "Europe/Moscow", "validUntil": "2026-12-01T00:00:00Z",
		},
	})
	require.NoError(t, c.dispatch(context.Background(), Envelope{EventID: "e2", EventType: "SubscriptionRenewed", Payload: payload}))
	require.Len(t, sink.tariffs, 1)
	assert.Equal(t, 2, sink.tariffs[0].WeekBitmap)
	assert.Equal(t, "Europe/Moscow", sink.tariffs[0].Timezone)
	assert.True(t, sink.tariffs[0].HasSnapshot)
	require.NotNil(t, sink.tariffs[0].ValidUntil)
}

func TestDispatchAccountDeleted(t *testing.T) {
	sink := &dispatchSink{}
	c := &Consumer{sink: sink, log: stubLogger{}}
	payload, _ := json.Marshal(map[string]any{"account_id": "11111111-1111-1111-1111-111111111111"})
	require.NoError(t, c.dispatch(context.Background(), Envelope{EventID: "e3", EventType: "AccountDeleted", Payload: payload}))
	assert.Equal(t, uuid.MustParse("11111111-1111-1111-1111-111111111111"), sink.deletions[0].AccountID)
}

func TestDecodeEnvelopeFallsBackToOffsetID(t *testing.T) {
	env, err := decodeEnvelope(msgWith(`{"event_type":"AccountDeleted","payload":{}}`))
	require.NoError(t, err)
	assert.Equal(t, "t:0:7", env.EventID)
}

func TestDeadLetterReportsPublishFailure(t *testing.T) {
	c := &Consumer{dlq: &recordingPublisher{err: assert.AnError}, dlqTopic: "saymo.deadletter", log: stubLogger{}}
	ok := c.deadLetter(context.Background(), msgWith(`{"event_type":"X"}`), Envelope{EventID: "e1", EventType: "X"}, assert.AnError)
	assert.False(t, ok, "consumer must not commit when the DLQ copy is not durable")
}

type failingSink struct{ accounts []string }

func (f *failingSink) OnAccountRegistered(_ context.Context, ev in.RegistrationEvent) error {
	f.accounts = append(f.accounts, ev.AccountID)
	return assert.AnError
}
func (f *failingSink) OnAccountDeleted(context.Context, in.AccountDeletedEvent) error { return nil }
func (f *failingSink) OnTariffChanged(context.Context, in.TariffEvent) error          { return nil }
func (f *failingSink) OnMigrationRequested(context.Context, in.MigrationEvent) error  { return nil }
func (f *failingSink) OnLessonAdded(context.Context, in.LessonAddedEvent) error       { return nil }

// TestHandleMessageStopsPartitionWhenDLQIsNotDurable reproduces the "skip commit, then commit N+1" trap:
// when the DLQ write fails, the caller must stop before processing (and later committing) the next message.
func TestHandleMessageStopsPartitionWhenDLQIsNotDurable(t *testing.T) {
	sink := &failingSink{}
	c := &Consumer{sink: sink, dlq: &recordingPublisher{err: assert.AnError}, dlqTopic: "dlq", log: stubLogger{}}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	payload1, _ := json.Marshal(map[string]any{
		"account_id": "11111111-1111-1111-1111-111111111111",
		"role":       "patient",
		"classifier": map[string]string{"difficulty": "easy", "aphasia_type": "motor_aphasia"},
	})
	payload2, _ := json.Marshal(map[string]any{
		"account_id": "22222222-2222-2222-2222-222222222222",
		"role":       "patient",
		"classifier": map[string]string{"difficulty": "easy", "aphasia_type": "motor_aphasia"},
	})
	msg1 := kafkago.Message{Topic: "t", Partition: 0, Offset: 1, Value: mustEnvelope(t, "e1", "AccountRegistered", payload1)}
	msg2 := kafkago.Message{Topic: "t", Partition: 0, Offset: 2, Value: mustEnvelope(t, "e2", "AccountRegistered", payload2)}

	var committedOffsets []int64
	for _, m := range []kafkago.Message{msg1, msg2} {
		if !c.handleMessage(ctx, m) {
			break
		}
		committedOffsets = append(committedOffsets, m.Offset)
	}
	assert.Empty(t, committedOffsets, "no offset may be committed when the DLQ copy is not durable")
	for _, account := range sink.accounts {
		assert.Equal(t, "11111111-1111-1111-1111-111111111111", account, "the next message must not be dispatched")
	}
}

func mustEnvelope(t *testing.T, id, eventType string, payload []byte) []byte {
	t.Helper()
	raw, err := BuildEnvelope(id, eventType, time.Now(), payload)
	require.NoError(t, err)
	return raw
}

func TestEventLag(t *testing.T) {
	env := Envelope{OccurredAt: time.Now().Add(-2 * time.Second).UTC().Format(time.RFC3339Nano)}
	assert.Greater(t, eventLag(env), 1.0)
	assert.Equal(t, float64(0), eventLag(Envelope{}))
}
