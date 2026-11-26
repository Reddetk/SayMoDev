package consumer

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeStore struct {
	applied []string
	deltas  []int
}

func (s *fakeStore) ApplyProgramEvent(_ context.Context, _, _, programID string, delta int) (bool, error) {
	s.applied = append(s.applied, programID)
	s.deltas = append(s.deltas, delta)
	return true, nil
}

func TestHandleAppliesDeltas(t *testing.T) {
	store := &fakeStore{}
	worker := NewWorker(nil, store, nil)

	for _, value := range []string{
		`{"event_id":"11111111-1111-4111-8111-111111111111","event_type":"ProgramActivated","program_id":"33333333-3333-4333-8333-333333333333"}`,
		`{"event_id":"22222222-2222-4222-8222-222222222222","event_type":"ProgramCompleted","program_id":"33333333-3333-4333-8333-333333333333"}`,
	} {
		if err := worker.handle(context.Background(), Message{Value: []byte(value)}); err != nil {
			t.Fatalf("handle: %v", err)
		}
	}

	if len(store.deltas) != 2 || store.deltas[0] != 1 || store.deltas[1] != -1 {
		t.Fatalf("deltas: %v", store.deltas)
	}
}

func TestHandleSkipsInvalidPayloadAndUUID(t *testing.T) {
	store := &fakeStore{}
	worker := NewWorker(nil, store, nil)

	cases := []string{
		`not-json`,
		`{"event_id":"e1","event_type":"ProgramActivated","program_id":"p1"}`,
		`{"event_id":"11111111-1111-4111-8111-111111111111","event_type":"Other","program_id":"33333333-3333-4333-8333-333333333333"}`,
	}
	for _, value := range cases {
		if err := worker.handle(context.Background(), Message{Value: []byte(value)}); err != nil {
			t.Fatalf("handle %q: %v", value, err)
		}
	}
	if len(store.applied) != 0 {
		t.Fatalf("invalid/unknown events must not reach store: %v", store.applied)
	}
}

type scriptedReader struct {
	messages []Message
	index    atomic.Int32
	commits  atomic.Int32
}

func (r *scriptedReader) ReadMessage(ctx context.Context) (Message, error) {
	// Как segmentio/kafka-go FetchMessage: позиция сдвигается в момент выдачи,
	// а не в Commit. Фейк должен это повторять, иначе тест не ловит потерю.
	index := int(r.index.Add(1)) - 1
	if index < len(r.messages) {
		return r.messages[index], nil
	}
	<-ctx.Done()
	return Message{}, ctx.Err()
}

func (r *scriptedReader) Commit(context.Context, Message) error {
	r.commits.Add(1)
	return nil
}

func (r *scriptedReader) Close() error { return nil }

type retryStore struct {
	attempts  atomic.Int32
	failUntil int32
	mu        sync.Mutex
	applied   []string
}

func (s *retryStore) ApplyProgramEvent(_ context.Context, _, _, programID string, _ int) (bool, error) {
	attempt := s.attempts.Add(1)
	if attempt <= s.failUntil {
		return false, errors.New("transient db error")
	}
	s.mu.Lock()
	s.applied = append(s.applied, programID)
	s.mu.Unlock()
	return true, nil
}

func (s *retryStore) appliedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.applied...)
}

func TestRunRetriesSameEventWithoutSkipping(t *testing.T) {
	reader := &scriptedReader{messages: []Message{
		{Value: []byte(`{"event_id":"11111111-1111-4111-8111-111111111111","event_type":"ProgramActivated","program_id":"33333333-3333-4333-8333-333333333333"}`)},
		{Value: []byte(`{"event_id":"22222222-2222-4222-8222-222222222222","event_type":"ProgramActivated","program_id":"44444444-4444-4444-8444-444444444444"}`)},
	}}
	store := &retryStore{failUntil: 1}
	worker := NewWorker(reader, store, nil)
	worker.retryBase = time.Millisecond
	worker.retryMax = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for reader.commits.Load() < 2 {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatalf("consumer did not recover: attempts=%d commits=%d applied=%v",
				store.attempts.Load(), reader.commits.Load(), store.appliedIDs())
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	<-done

	if store.attempts.Load() < 3 {
		t.Fatalf("failed event must be retried: attempts=%d", store.attempts.Load())
	}
	if reader.commits.Load() != 2 {
		t.Fatalf("both events must be committed once, commits=%d", reader.commits.Load())
	}
	applied := store.appliedIDs()
	if len(applied) != 2 || applied[0] != "33333333-3333-4333-8333-333333333333" || applied[1] != "44444444-4444-4444-8444-444444444444" {
		t.Fatalf("failed event must not be skipped, applied=%v", applied)
	}
}
