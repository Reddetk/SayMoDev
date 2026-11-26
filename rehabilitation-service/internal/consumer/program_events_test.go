package consumer

import (
	"context"
	"testing"
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
