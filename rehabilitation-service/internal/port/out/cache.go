// goca:manual — порт L2-кэша; usecase не должен зависеть от адаптера Redis.
package out

import (
	"context"
	"time"
)

// Cache — вторичный порт L2-кэша (Redis в проде).
type Cache interface {
	Get(ctx context.Context, key string, dst any) (bool, error)
	Set(ctx context.Context, key string, value any, ttl time.Duration) error
	Delete(ctx context.Context, keys ...string) error
	DeletePrefix(ctx context.Context, prefix string) error
}
