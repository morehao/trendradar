package summarize

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// Cache stores summaries keyed by (fullName, sha256(description)) with a TTL.
// It is in-memory only; it never touches the core's on-disk store.
type Cache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
	ttl     time.Duration
}

type cacheEntry struct {
	summary string
	expires time.Time
}

// NewCache returns a cache with a 30-day TTL.
func NewCache() *Cache {
	return &Cache{entries: make(map[string]cacheEntry), ttl: 30 * 24 * time.Hour}
}

func cacheKey(fullName, desc string) string {
	h := sha256.Sum256([]byte(fullName + "\x00" + desc))
	return hex.EncodeToString(h[:])
}

// Get returns a cached summary if present and unexpired.
func (c *Cache) Get(fullName, desc string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[cacheKey(fullName, desc)]
	if !ok || time.Now().After(e.expires) {
		return "", false
	}
	return e.summary, true
}

// Put stores a summary.
func (c *Cache) Put(fullName, desc, summary string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[cacheKey(fullName, desc)] = cacheEntry{summary: summary, expires: time.Now().Add(c.ttl)}
}
