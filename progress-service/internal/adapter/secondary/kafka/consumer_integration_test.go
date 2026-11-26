//go:build integration

package kafka

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Reddetk/SayMoDev/progress-service/internal/port/in"
)

type integrationLogger struct{}

func (integrationLogger) Debug(string, ...zap.Field) {}
func (integrationLogger) Info(string, ...zap.Field)  {}
func (integrationLogger) Warn(string, ...zap.Field)  {}
func (integrationLogger) Error(string, ...zap.Field) {}
func (integrationLogger) Fatal(string, ...zap.Field) {}

type sinkRecorder struct {
	mu   sync.Mutex
	regs []in.RegistrationEvent
}

func (s *sinkRecorder) OnAccountRegistered(_ context.Context, ev in.RegistrationEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.regs = append(s.regs, ev)
	return nil
}
func (s *sinkRecorder) OnAccountDeleted(context.Context, in.AccountDeletedEvent) error { return nil }
func (s *sinkRecorder) OnTariffChanged(context.Context, in.TariffEvent) error          { return nil }
func (s *sinkRecorder) OnMigrationRequested(context.Context, in.MigrationEvent) error  { return nil }
func (s *sinkRecorder) OnLessonAdded(context.Context, in.LessonAddedEvent) error       { return nil }

func (s *sinkRecorder) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.regs)
}

// TestConsumerDispatchesAccountRegistered requires a live Kafka broker:
//
//	KAFKA_BROKERS=localhost:9094 go test -tags=integration ./internal/adapter/secondary/kafka -run Kafka -v
func TestConsumerDispatchesAccountRegistered(t *testing.T) {
	brokers := os.Getenv("KAFKA_BROKERS")
	if brokers == "" {
		t.Skip("KAFKA_BROKERS is not set")
	}
	topic := "progress.test.identity." + uuid.NewString()
	dlq := "progress.test.dlq." + uuid.NewString()
	accountID := uuid.NewString()

	require.NoError(t, EnsureTopics([]string{brokers}, topic, dlq))
	writer := &kafkago.Writer{Addr: kafkago.TCP(brokers), Topic: topic, RequiredAcks: kafkago.RequireAll, BatchTimeout: 10 * time.Millisecond}
	payload := map[string]any{
		"account_id": accountID,
		"role":       "patient",
		"classifier": map[string]string{"difficulty": "easy", "aphasia_type": "motor_aphasia"},
	}
	env, err := BuildEnvelope(uuid.NewString(), "AccountRegistered", time.Now(), mustJSON(t, payload))
	require.NoError(t, err)
	require.NoError(t, writer.WriteMessages(context.Background(), kafkago.Message{Key: []byte(accountID), Value: env}))
	t.Cleanup(func() { _ = writer.Close() })

	sink := &sinkRecorder{}
	pub := NewProducer([]string{brokers})
	t.Cleanup(func() { _ = pub.Close() })
	consumer := NewConsumer([]string{brokers}, "progress-test-"+uuid.NewString(), []string{topic}, sink, pub, dlq, integrationLogger{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { consumer.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); _ = consumer.Close(); <-done })

	require.Eventually(t, func() bool { return sink.count() == 1 }, 20*time.Second, 200*time.Millisecond)
	sink.mu.Lock()
	defer sink.mu.Unlock()
	assert.Equal(t, accountID, sink.regs[0].AccountID)
	assert.Equal(t, "easy", sink.regs[0].Difficulty)
	assert.Equal(t, "motor_aphasia", sink.regs[0].AphasiaType)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return raw
}
