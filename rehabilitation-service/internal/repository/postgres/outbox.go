// goca:manual — outbox store и идемпотентный consumer-store BC#3.
package postgres

import (
	"context"
	"fmt"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/outbox"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OutboxStore struct {
	pool *pgxpool.Pool
}

func NewOutboxStore(pool *pgxpool.Pool) *OutboxStore { return &OutboxStore{pool: pool} }

func (s *OutboxStore) FetchUnsent(ctx context.Context, limit int) ([]outbox.Message, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, event_type, aggregate_id, payload
FROM outbox WHERE sent_at IS NULL
ORDER BY created_at
LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("fetch outbox: %w", err)
	}
	defer rows.Close()

	var messages []outbox.Message
	for rows.Next() {
		var message outbox.Message
		if err := rows.Scan(&message.ID, &message.EventType, &message.AggregateID, &message.Payload); err != nil {
			return nil, fmt.Errorf("scan outbox: %w", err)
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *OutboxStore) MarkSent(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `UPDATE outbox SET sent_at = now() WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return fmt.Errorf("mark outbox sent: %w", err)
	}
	return nil
}

type ProgramEventStore struct {
	pool *pgxpool.Pool
}

func NewProgramEventStore(pool *pgxpool.Pool) *ProgramEventStore {
	return &ProgramEventStore{pool: pool}
}

// ApplyProgramEvent идемпотентно меняет active_reference_count; floor 0.
func (s *ProgramEventStore) ApplyProgramEvent(ctx context.Context, eventID, eventType, programID string, delta int) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin event tx: %w", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
INSERT INTO processed_events (event_id, event_type) VALUES ($1, $2)
ON CONFLICT (event_id) DO NOTHING`, eventID, eventType)
	if err != nil {
		return false, fmt.Errorf("record processed event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		return false, nil
	}

	if _, err := tx.Exec(ctx, `
UPDATE programs
SET active_reference_count = GREATEST(0, active_reference_count + $2), updated_at = now()
WHERE id = $1`, programID, delta); err != nil {
		return false, fmt.Errorf("update reference count: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit event tx: %w", err)
	}
	return true, nil
}
