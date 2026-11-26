package outbox

import (
	"context"
	"errors"
	"testing"
)

type fakeStore struct {
	messages []Message
	sent     []string
	fetchErr error
}

func (s *fakeStore) FetchUnsent(context.Context, int) ([]Message, error) {
	return s.messages, s.fetchErr
}
func (s *fakeStore) MarkSent(_ context.Context, ids []string) error {
	s.sent = append(s.sent, ids...)
	return nil
}

type fakePublisher struct {
	published []string
	failAt    int
}

func (p *fakePublisher) Publish(_ context.Context, _, key string, _ []byte) error {
	if p.failAt > 0 && len(p.published) == p.failAt {
		return errors.New("kafka down")
	}
	p.published = append(p.published, key)
	return nil
}

func TestDrainPublishesAndMarksSent(t *testing.T) {
	store := &fakeStore{messages: []Message{{ID: "e1", AggregateID: "p1", Payload: []byte(`{}`)}, {ID: "e2", AggregateID: "p2"}}}
	publisher := &fakePublisher{}
	worker := NewWorker(store, publisher, "saymo.content.events", nil)

	count, err := worker.Drain(context.Background())
	if err != nil || count != 2 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if len(store.sent) != 2 || store.sent[0] != "e1" {
		t.Fatalf("sent: %v", store.sent)
	}
}

func TestDrainMarksOnlyPublishedOnPartialFailure(t *testing.T) {
	store := &fakeStore{messages: []Message{{ID: "e1", AggregateID: "p1"}, {ID: "e2", AggregateID: "p2"}}}
	publisher := &fakePublisher{failAt: 1}
	worker := NewWorker(store, publisher, "saymo.content.events", nil)

	count, err := worker.Drain(context.Background())
	if err == nil {
		t.Fatal("want publish error")
	}
	if count != 1 || len(store.sent) != 1 || store.sent[0] != "e1" {
		t.Fatalf("count=%d sent=%v", count, store.sent)
	}
}
