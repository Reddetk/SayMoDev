// goca:manual — Kafka producer/consumer adapters BC#3 (segmentio/kafka-go).
package kafka

import (
	"context"
	"fmt"
	"time"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/consumer"
	"github.com/segmentio/kafka-go"
)

type Producer struct {
	writer *kafka.Writer
}

func NewProducer(brokers []string) *Producer {
	return &Producer{writer: &kafka.Writer{
		Addr:         kafka.TCP(brokers...),
		Balancer:     &kafka.Hash{},
		RequiredAcks: kafka.RequireAll,
		BatchTimeout: 10 * time.Millisecond,
	}}
}

func (p *Producer) Publish(ctx context.Context, topic, key string, value []byte) error {
	if err := p.writer.WriteMessages(ctx, kafka.Message{
		Topic: topic,
		Key:   []byte(key),
		Value: value,
	}); err != nil {
		return fmt.Errorf("kafka publish: %w", err)
	}
	return nil
}

func (p *Producer) Close() error { return p.writer.Close() }

type Consumer struct {
	reader *kafka.Reader
}

func NewConsumer(brokers []string, groupID, topic string) *Consumer {
	return &Consumer{reader: kafka.NewReader(kafka.ReaderConfig{
		Brokers:        brokers,
		GroupID:        groupID,
		Topic:          topic,
		MinBytes:       1,
		MaxBytes:       10 << 20,
		CommitInterval: 0, // commit только после успешной обработки
	})}
}

func (c *Consumer) ReadMessage(ctx context.Context) (consumer.Message, error) {
	message, err := c.reader.FetchMessage(ctx)
	if err != nil {
		return consumer.Message{}, err
	}
	return consumer.Message{
		Topic:     message.Topic,
		Partition: message.Partition,
		Offset:    message.Offset,
		Key:       message.Key,
		Value:     message.Value,
	}, nil
}

func (c *Consumer) Commit(ctx context.Context, message consumer.Message) error {
	return c.reader.CommitMessages(ctx, kafka.Message{
		Topic:     message.Topic,
		Partition: message.Partition,
		Offset:    message.Offset,
		Key:       message.Key,
		Value:     message.Value,
	})
}

func (c *Consumer) Close() error { return c.reader.Close() }

// Lag отдаёт отставание консьюмера для kafka_consumer_queue_length.
func (c *Consumer) Lag() int64 { return c.reader.Stats().Lag }
