// Package cache — L1 in-process кэш BC#3 (TTL, без внешних зависимостей).
// goca:manual — L1+L2 стратегия docs BC#3: ADR-011 задаёт 60s/5min (граница staleness 5 мин).
package cache

import (
	"sync"
	"time"
)

const defaultMaxEntries = 1024

type entry struct {
	value   any
	expires time.Time
}

type Cache struct {
	mu         sync.RWMutex
	entries    map[string]entry
	maxEntries int
	now        func() time.Time
}

func New() *Cache {
	return &Cache{entries: map[string]entry{}, maxEntries: defaultMaxEntries, now: time.Now}
}

// NewWithLimit — L1 с ограничением числа ключей (защита публичного API от роста памяти).
func NewWithLimit(maxEntries int) *Cache {
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}
	return &Cache{entries: map[string]entry{}, maxEntries: maxEntries, now: time.Now}
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
		// Удаляем только если запись всё ещё просрочена: параллельный Set мог её заменить.
		if current, exists := c.entries[key]; exists && c.now().After(current.expires) {
			delete(c.entries, key)
		}
		c.mu.Unlock()
		return nil, false
	}
	return item.value, true
}

func (c *Cache) Set(key string, value any, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.maxEntries {
		c.evictLocked()
	}
	c.entries[key] = entry{value: value, expires: c.now().Add(ttl)}
}

// DeletePrefix удаляет ключи с префиксом (инвалидация после write).
func (c *Cache) DeletePrefix(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.entries {
		if prefix == "" || len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			delete(c.entries, key)
		}
	}
}

func (c *Cache) Clear() { c.DeletePrefix("") }

// evictLocked удаляет просроченные записи, затем самую раннюю по expiry.
func (c *Cache) evictLocked() {
	now := c.now()
	for k, v := range c.entries {
		if now.After(v.expires) {
			delete(c.entries, k)
		}
	}
	if len(c.entries) < c.maxEntries {
		return
	}

	var oldestKey string
	var oldest time.Time
	for k, v := range c.entries {
		if oldestKey == "" || v.expires.Before(oldest) {
			oldestKey, oldest = k, v.expires
		}
	}
	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}
