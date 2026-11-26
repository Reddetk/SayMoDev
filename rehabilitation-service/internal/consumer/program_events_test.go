package consumer

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeReader struct {
	messages []Message
	idx      int
	commits  int
}

func (r *fakeReader) ReadMessage(context.Context) (Message, error) {
	if r.idx >= len(r.messages) {
		time.Sleep(time.Millisecond)
		return Message{}, errors.New("EOF")
	}
	message := r.messages[r.idx]
	r.idx++
	return message, nil
}
func (r *fakeReader) Commit(context.Context, Message) error { r.commits++; return nil }
func (r *fakeReader) Close() error                          { return nil }

type fakeStore struct {
	applied []string
	delta   int
}

func (s *fakeStore) ApplyProgramEvent(_ context.Context, eventID, _, programID string, delta int) (bool, error) {
	s.applied = append(s.applied, programID)
	s.delta = delta
	return true, nil
}

func TestWorkerAppliesActivatedAndCompleted(t *testing.T) {
	reader := &fakeReader{messages: []Message{
		{Value: []byte(`{"event_id":"e1","event_type":"ProgramActivated","program_id":"p1"}`)},
		{Value: []byte(`{"event_id":"e2","event_type":"ProgramCompleted","program_id":"p1"}`)},
	}}
	store := &fakeStore{}
	worker := NewWorker(reader, store, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for reader.idx < len(reader.messages) {
		}
		cancel()
	}()
	worker.Run(ctx)

	if len(store.applied) != 2 {
		t.Fatalf("applied: %v", store.applied)
	}
}

type deltaStore struct{ deltas []int }

func (s *deltaStore) ApplyProgramEvent(_ context.Context, _, _, _ string, delta int) (bool, error) {
	s.deltas = append(s.deltas, delta)
	return true, nil
}

func TestDeltasForEvents(t *testing.T) {
	reader := &fakeReader{messages: []Message{
		{Value: []byte(`{"event_id":"e1","event_type":"ProgramActivated","program_id":"p1"}`)},
		{Value: []byte(`{"event_id":"e2","event_type":"ProgramCompleted","program_id":"p1"}`)},
	}}
	store := &deltaStore{}
	worker := NewWorker(reader, store, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for reader.idx < len(reader.messages) {
		}
		cancel()
	}()
	worker.Run(ctx)

	if len(store.deltas) != 2 || store.deltas[0] != 1 || store.deltas[1] != -1 {
		t.Fatalf("deltas: %v", store.deltas)
	}
}
