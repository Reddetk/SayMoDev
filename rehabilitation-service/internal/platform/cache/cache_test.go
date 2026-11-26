package cache

import (
	"testing"
	"time"
)

func TestCacheEvictsWhenFull(t *testing.T) {
	c := NewWithLimit(3)
	for i := 0; i < 10; i++ {
		c.Set(string(rune('a'+i)), i, time.Minute)
	}
	if len(c.entries) > 3 {
		t.Fatalf("entries: want <=3, got %d", len(c.entries))
	}
}

func TestExpiredEntryIsNotServed(t *testing.T) {
	c := New()
	now := time.Now()
	c.now = func() time.Time { return now }
	c.Set("k", "v", time.Second)

	now = now.Add(2 * time.Second)
	if _, ok := c.Get("k"); ok {
		t.Fatal("expired entry must be a miss")
	}
}
