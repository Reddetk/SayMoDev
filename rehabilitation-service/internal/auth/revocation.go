// goca:manual — shared redis-blacklist BC#3; генератор не знает топологию BC.
package auth

import (
	"context"
	"errors"
	"fmt"

	goredis "github.com/redis/go-redis/v9"
)

const (
	jtiBlacklistKeyPrefix = "jwt:black:"
	accountRevKeyPrefix   = "account:rev:"
)

// RedisRevocation — L2-хранилище отзыва в shared redis-blacklist.
type RedisRevocation struct {
	client *goredis.Client
}

func NewRedisRevocation(client *goredis.Client) *RedisRevocation {
	return &RedisRevocation{client: client}
}

func (r *RedisRevocation) JTIBlacklisted(ctx context.Context, jti string) (bool, error) {
	exists, err := r.client.Exists(ctx, jtiBlacklistKeyPrefix+jti).Result()
	if err != nil {
		return false, err
	}
	return exists > 0, nil
}

func (r *RedisRevocation) AccountRev(ctx context.Context, sub string) (int, bool, error) {
	rev, err := r.client.Get(ctx, accountRevKeyPrefix+sub).Int()
	if errors.Is(err, goredis.Nil) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("get account rev: %w", err)
	}
	return rev, true, nil
}
