// Package redis implements the L2 cache, idempotency store and revocation projection adapters.
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Reddetk/SayMoDev/progress-service/internal/adapter/telemetry"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

// Cache provides L1 in-process + L2 Redis, as required by the BC#4 read path.
type Cache struct {
	l1       *localCache
	l2       *redis.Client
	l1TTL    time.Duration
	keySpace string
}

// NewCache builds the two-level cache.
func NewCache(client *redis.Client, l1TTL time.Duration) *Cache {
	return &Cache{l1: newLocalCache(10_000, l1TTL), l2: client, l1TTL: l1TTL}
}

func (c *Cache) Get(ctx context.Context, key string) (string, bool, error) {
	if v, ok := c.l1.get(key); ok {
		return v, true, nil
	}
	v, err := c.l2.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	c.l1.set(key, v, c.l1TTL)
	return v, true, nil
}

func (c *Cache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	c.l1.set(key, value, minDuration(ttl, c.l1TTL))
	if err := c.l2.Set(ctx, key, value, ttl).Err(); err != nil {
		telemetry.RedisWriteFailuresTotal.WithLabelValues("set").Inc()
		return err
	}
	return nil
}

func (c *Cache) Del(ctx context.Context, keys ...string) error {
	for _, k := range keys {
		c.l1.del(k)
	}
	if len(keys) == 0 {
		return nil
	}
	if err := c.l2.Del(ctx, keys...).Err(); err != nil {
		telemetry.RedisWriteFailuresTotal.WithLabelValues("del").Inc()
		return err
	}
	return nil
}

func (c *Cache) Ping(ctx context.Context) error { return c.l2.Ping(ctx).Err() }

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

type entry struct {
	value   string
	expires time.Time
}

type localCache struct {
	mu    sync.RWMutex
	items map[string]entry
	max   int
	ttl   time.Duration
}

func newLocalCache(max int, ttl time.Duration) *localCache {
	return &localCache{items: make(map[string]entry), max: max, ttl: ttl}
}

func (c *localCache) get(key string) (string, bool) {
	c.mu.RLock()
	e, ok := c.items[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.expires) {
		return "", false
	}
	return e.value, true
}

func (c *localCache) set(key, value string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= c.max {
		// KISS eviction: drop the whole map rather than maintain an LRU list.
		c.items = make(map[string]entry, c.max)
	}
	c.items[key] = entry{value: value, expires: time.Now().Add(ttl)}
}

func (c *localCache) del(key string) {
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
}

// Idempotency implements the T1 idempotency invariant on Redis with a 24h TTL.
type Idempotency struct {
	client *redis.Client
}

// NewIdempotency builds the idempotency store.
func NewIdempotency(client *redis.Client) *Idempotency { return &Idempotency{client: client} }

type storedResponse struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

func idemKey(key string) string { return "idem:" + key }

func (i *Idempotency) Begin(ctx context.Context, key string, ttl time.Duration) (out.IdempotencyState, *out.StoredResponse, error) {
	k := idemKey(key)
	ok, err := i.client.SetNX(ctx, k, "in_progress", ttl).Result()
	if err != nil {
		return out.IdempotencyNew, nil, err
	}
	if ok {
		return out.IdempotencyNew, nil, nil
	}
	raw, err := i.client.Get(ctx, k).Result()
	if errors.Is(err, redis.Nil) {
		// Expired between SETNX and GET: treat as a fresh request.
		return out.IdempotencyNew, nil, nil
	}
	if err != nil {
		return out.IdempotencyNew, nil, err
	}
	if raw == "in_progress" {
		return out.IdempotencyInProgress, nil, nil
	}
	var sr storedResponse
	if err := json.Unmarshal([]byte(raw), &sr); err != nil {
		return out.IdempotencyInProgress, nil, nil
	}
	return out.IdempotencyCompleted, &out.StoredResponse{Status: sr.Status, Body: []byte(sr.Body)}, nil
}

func (i *Idempotency) Complete(ctx context.Context, key string, resp out.StoredResponse, ttl time.Duration) error {
	raw, err := json.Marshal(storedResponse{Status: resp.Status, Body: string(resp.Body)})
	if err != nil {
		return err
	}
	return i.client.Set(ctx, idemKey(key), raw, ttl).Err()
}

func (i *Idempotency) Release(ctx context.Context, key string) error {
	return i.client.Del(ctx, idemKey(key)).Err()
}

func (i *Idempotency) Ping(ctx context.Context) error { return i.client.Ping(ctx).Err() }

// Revocation reads the shared redis-blacklist owned by BC#1.
type Revocation struct {
	client *redis.Client
}

// NewRevocation builds the shared revocation reader.
func NewRevocation(client *redis.Client) *Revocation { return &Revocation{client: client} }

func (r *Revocation) IsJTIBlacklisted(ctx context.Context, jti string) (bool, error) {
	n, err := r.client.Exists(ctx, "jwt:black:"+jti).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (r *Revocation) AccountRev(ctx context.Context, accountID string) (int64, bool, error) {
	v, err := r.client.Get(ctx, "account:rev:"+accountID).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return v, true, nil
}

func (r *Revocation) Ping(ctx context.Context) error { return r.client.Ping(ctx).Err() }

var (
	_ out.Cache            = (*Cache)(nil)
	_ out.IdempotencyStore = (*Idempotency)(nil)
	_ out.RevocationStore  = (*Revocation)(nil)
)
