// Package redis — L2-адаптер BC#2 (idempotency, quotes, rate limit, revocation).
package redis

// goca:manual — Redis-адаптер BC#2; goca cache-слой не покрывает §12/ADR redis topology.

import (
	"context"
	"fmt"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Cache реализует out.Cache.
type Cache struct {
	own       *goredis.Client
	blacklist *goredis.Client
}

// New создаёт Cache. blacklist может быть nil только в тестах (fail-closed в prod).
func New(own, blacklist *goredis.Client) *Cache {
	return &Cache{own: own, blacklist: blacklist}
}

// Get читает значение.
func (c *Cache) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := c.own.Get(ctx, key).Bytes()
	if err != nil {
		return nil, err
	}
	return value, nil
}

// Set пишет значение с TTL.
func (c *Cache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.own.Set(ctx, key, value, ttl).Err()
}

const rateLimitScript = `
local count = redis.call('GET', KEYS[1])
if count and tonumber(count) >= 3 then return 0 end
local last = redis.call('GET', KEYS[2])
if last and (tonumber(ARGV[1]) - tonumber(last)) < 3600 then return 0 end
redis.call('INCR', KEYS[1])
redis.call('EXPIRE', KEYS[1], 86400)
redis.call('SET', KEYS[2], ARGV[1], 'EX', 86400)
return 1
`

// AllowUpgradeChange реализует §12: ≤3 смены/24ч, ≥1ч между сменами.
func (c *Cache) AllowUpgradeChange(ctx context.Context, accountID string, now time.Time) (bool, error) {
	keys := []string{
		"billing:plan_change:count:" + accountID,
		"billing:plan_change:last:" + accountID,
	}
	result, err := c.own.Eval(ctx, rateLimitScript, keys, strconv.FormatInt(now.Unix(), 10)).Int()
	if err != nil {
		return false, fmt.Errorf("rate limit: %w", err)
	}
	return result == 1, nil
}

// IsRevoked проверяет jti blacklist и account:rev в shared Redis (BC#1).
func (c *Cache) IsRevoked(ctx context.Context, jti, accountID string, tokenRev int) (bool, error) {
	if c.blacklist == nil {
		return false, fmt.Errorf("blacklist redis is not configured")
	}
	blacklisted, err := c.blacklist.Exists(ctx, "jwt:black:"+jti).Result()
	if err != nil {
		return false, fmt.Errorf("blacklist check: %w", err)
	}
	if blacklisted > 0 {
		return true, nil
	}
	rawRev, err := c.blacklist.Get(ctx, "account:rev:"+accountID).Result()
	if err == goredis.Nil {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("account rev check: %w", err)
	}
	currentRev, err := strconv.Atoi(rawRev)
	if err != nil {
		return false, fmt.Errorf("account rev parse: %w", err)
	}
	return tokenRev < currentRev, nil
}
