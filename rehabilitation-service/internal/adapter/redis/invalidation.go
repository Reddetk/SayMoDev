// Package redis — L2-кэш и pub/sub-инвалидация L1 между репликами BC#3.
package redis

import (
	"context"

	goredis "github.com/redis/go-redis/v9"
)

// InvalidationChannel — канал широковещательной инвалидации L1 (ADR-011).
const InvalidationChannel = "bc3:cache:invalidate"

type Invalidator struct {
	client *goredis.Client
}

func NewInvalidator(client *goredis.Client) *Invalidator {
	return &Invalidator{client: client}
}

// PublishInvalidation отправляет scope всем репликам. Потеря сообщения безопасна:
// staleness ограничен TTL L1/L2 (reconciliation path).
func (i *Invalidator) PublishInvalidation(ctx context.Context, scope string) error {
	return i.client.Publish(ctx, InvalidationChannel, scope).Err()
}

// Run слушает канал до отмены ctx. go-redis переподключается сам; при закрытии
// канала цикл завершается, чтобы владелец мог перезапустить подписчика.
func (i *Invalidator) Run(ctx context.Context, apply func(scope string)) {
	pubsub := i.client.Subscribe(ctx, InvalidationChannel)
	defer pubsub.Close()

	messages := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-messages:
			if !ok {
				return
			}
			apply(message.Payload)
		}
	}
}
