// Command eventgen publishes a domain event envelope to Kafka for the local test bench.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"
)

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func isFlagPassed(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func main() {
	topic := flag.String("topic", "saymo.identity.events", "Kafka topic")
	eventType := flag.String("type", "AccountRegistered", "event_type")
	account := flag.String("account", "", "accountId")
	role := flag.String("role", "patient", "role (AccountRegistered)")
	difficulty := flag.String("difficulty", "easy", "classifier difficulty")
	aphasia := flag.String("aphasia", "motor_aphasia", "classifier aphasiaType")
	weekBitmap := flag.Int("weekbitmap", 127, "weekBitmap (tariff events)")
	timezone := flag.String("timezone", "UTC", "IANA timezone (tariff events)")
	validUntil := flag.String("validuntil", "", "RFC3339 validUntil (empty = indefinite)")
	programID := flag.String("program", "", "programId (migration events)")
	flag.Parse()

	if *account == "" {
		*account = uuid.NewString()
	}
	if !isFlagPassed("topic") {
		switch *eventType {
		case "AccountRegistered":
			*topic = "saymo.identity.events"
		case "MigrationRequested", "LessonAddedToProgram":
			*topic = "saymo.content.events"
		default:
			*topic = "saymo.billing.events"
		}
	}
	brokers := os.Getenv("KAFKA_BROKERS")
	if brokers == "" {
		brokers = "localhost:9094"
	}

	payload := map[string]any{"accountId": *account}
	switch *eventType {
	case "AccountRegistered":
		payload["role"] = *role
		payload["registrationMethod"] = "email"
		payload["classifier"] = map[string]string{"difficulty": *difficulty, "aphasiaType": *aphasia}
	case "TariffPlanActivated", "TariffPlanChanged", "SubscriptionActivated", "SubscriptionChanged":
		snapshot := map[string]any{"weekBitmap": *weekBitmap, "timezone": *timezone}
		if *validUntil != "" {
			snapshot["validUntil"] = *validUntil
		}
		payload["tariffSnapshot"] = snapshot
	case "MigrationRequested":
		payload["programId"] = *programID
		payload["targetVersion"] = 1
		payload["accountIds"] = []string{*account}
	default:
		payload["accountId"] = *account
	}

	env := map[string]any{
		"event_id":    uuid.NewString(),
		"event_type":  *eventType,
		"occurred_at": time.Now().UTC().Format(time.RFC3339Nano),
		"payload":     payload,
	}
	raw, err := jsonMarshal(env)
	if err != nil {
		log.Fatal(err)
	}
	w := &kafkago.Writer{Addr: kafkago.TCP(brokers), Topic: *topic, RequiredAcks: kafkago.RequireAll}
	if err := w.WriteMessages(context.Background(), kafkago.Message{Key: []byte(*account), Value: raw}); err != nil {
		log.Fatalf("eventgen: %v", err)
	}
	_ = w.Close()
	log.Printf("published %s for account %s to %s", *eventType, *account, *topic)
}
