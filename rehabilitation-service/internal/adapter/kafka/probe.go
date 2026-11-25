// Package kafka — адаптер Kafka и его readiness-проба.
// goca:manual — outbox/consumer-транспорт BC#3 генератор не создаёт.
package kafka

import (
	"context"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"
)

type Probe struct {
	brokers []string
	timeout time.Duration
}

func New(brokers []string, timeout time.Duration) *Probe {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &Probe{brokers: brokers, timeout: timeout}
}

func (p *Probe) Name() string { return "kafka" }

// Ping требует успешного metadata-запроса хотя бы от одного брокера.
// Deadline ставится на само соединение: DialContext не ограничивает чтение,
// поэтому без SetDeadline зависший брокер блокировал бы /ready навсегда.
func (p *Probe) Ping(ctx context.Context) error {
	var lastErr error
	for _, broker := range p.brokers {
		conn, err := kafka.DialContext(ctx, "tcp", broker)
		if err != nil {
			lastErr = err
			continue
		}
		_ = conn.SetDeadline(time.Now().Add(p.timeout))
		_, err = conn.ReadPartitions()
		_ = conn.Close()
		if err == nil {
			return nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no brokers configured")
	}
	return fmt.Errorf("kafka metadata: %w", lastErr)
}

func (p *Probe) Brokers() []string { return p.brokers }
