// Package cache — L1 in-process кэш BC#3 (TTL, без внешних зависимостей).
// goca:manual — L1+L2 стратегия docs BC#3 (5min/1h) генератором не создаётся.
package cache

import (
	"sync"
	"time"
)

type entry struct {
	value   any
	expires time.Time
}

type Cache struct {
	mu      sync.RWMutex
	entries map[string]entry
	now     func() time.Time
}

func New() *Cache {
	return &Cache{entries: map[string]entry{}, now: time.Now}
}

func (c *Cache) Get(key string) (any, bool) {
	c.mu.RLock()
	item, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if c.now().After(item.expires) {
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		return nil, false
	}
	return item.value, true
}

func (c *Cache) Set(key string, value any, ttl time.Duration) {
	c.mu.Lock()
	c.entries[key] = entry{value: value, expires: c.now().Add(ttl)}
	c.mu.Unlock()
}
