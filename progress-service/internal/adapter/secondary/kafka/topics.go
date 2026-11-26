package kafka

import (
	"errors"
	"fmt"

	kafkago "github.com/segmentio/kafka-go"
)

// EnsureTopics creates topics when missing (idempotent). Local/test bench only needs one broker.
func EnsureTopics(brokers []string, topics ...string) error {
	if len(brokers) == 0 {
		return nil
	}
	conn, err := kafkago.Dial("tcp", brokers[0])
	if err != nil {
		return fmt.Errorf("kafka: dial %s: %w", brokers[0], err)
	}
	defer conn.Close()

	configs := make([]kafkago.TopicConfig, 0, len(topics))
	for _, topic := range topics {
		if topic == "" {
			continue
		}
		configs = append(configs, kafkago.TopicConfig{Topic: topic, NumPartitions: 1, ReplicationFactor: 1})
	}
	if len(configs) == 0 {
		return nil
	}
	err = conn.CreateTopics(configs...)
	if err != nil && !errors.Is(err, kafkago.TopicAlreadyExists) {
		return fmt.Errorf("kafka: create topics: %w", err)
	}
	return nil
}
