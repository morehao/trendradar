package enrich

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DefaultCacheTTL is how long a fetched topics/README context stays reusable.
// Repo topics and README rarely change day-to-day, so a week keeps GitHub
// traffic bounded without serving badly stale text.
const DefaultCacheTTL = 168 * time.Hour

// Cache stores per-repository external context keyed by (fullName, mode) with a
// TTL. Repo and readme modes are keyed separately so turning readme on never
// reuses a topics-only entry (and vice versa).
type Cache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
	ttl     time.Duration
}

type cacheEntry struct {
	Topics        []string  `json:"topics,omitempty"`
	ReadmeExcerpt string    `json:"readmeExcerpt,omitempty"`
	FetchedAt     time.Time `json:"fetchedAt"`
	Source        string    `json:"source,omitempty"`
	Expires       time.Time `json:"expires"`
}

type cacheFile struct {
	Version int                   `json:"version"`
	Entries map[string]cacheEntry `json:"entries"`
}

const cacheFileVersion = 1

// NewCache returns an empty cache with the default 7-day TTL.
func NewCache() *Cache {
	return &Cache{entries: make(map[string]cacheEntry), ttl: DefaultCacheTTL}
}

// SetTTL overrides the entry TTL (tests / operator tuning).
func (c *Cache) SetTTL(ttl time.Duration) {
	if c == nil || ttl <= 0 {
		return
	}
	c.mu.Lock()
	c.ttl = ttl
	c.mu.Unlock()
}

// CachePath returns the on-disk enrich cache location for a work tree. It sits
// next to the summary cache under .git so it is never committed.
func CachePath(repoDir string) string {
	return filepath.Join(repoDir, ".git", "trendradar-enrich-cache.json")
}

// LoadCache reads a persisted cache. A missing/empty path or a corrupt file
// yields a fresh empty cache: the cache is only an optimization, never a source
// of truth, so damage must not fail a run. An empty path returns (nil, nil).
func LoadCache(path string) (*Cache, error) {
	if path == "" {
		return nil, nil
	}
	c := NewCache()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	var f cacheFile
	if err := json.Unmarshal(data, &f); err != nil || f.Version != cacheFileVersion {
		return c, nil
	}
	now := time.Now()
	for k, e := range f.Entries {
		if now.After(e.Expires) {
			continue
		}
		c.entries[k] = e
	}
	return c, nil
}

func (c *Cache) key(fullName string, mode Mode) string {
	return fullName + "\x00" + string(mode)
}

// get returns a cached context if present and unexpired.
func (c *Cache) get(fullName string, mode Mode) (Context, bool) {
	if c == nil {
		return Context{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[c.key(fullName, mode)]
	if !ok || time.Now().After(e.Expires) {
		return Context{}, false
	}
	return Context{Topics: e.Topics, ReadmeExcerpt: e.ReadmeExcerpt}, true
}

// put stores a context; empty contexts are still cached so a repo with no
// topics is not re-fetched on every run.
func (c *Cache) put(fullName string, mode Mode, ctx Context) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.entries[c.key(fullName, mode)] = cacheEntry{
		Topics:        ctx.Topics,
		ReadmeExcerpt: ctx.ReadmeExcerpt,
		FetchedAt:     now,
		Source:        "api",
		Expires:       now.Add(c.ttl),
	}
}

// Len reports how many unexpired entries the cache holds (tests/diagnostics).
func (c *Cache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	now := time.Now()
	for _, e := range c.entries {
		if now.Before(e.Expires) {
			n++
		}
	}
	return n
}

// Save writes the cache atomically (temp file + rename), dropping expired
// entries first. The file is 0600 because it holds only public repo data but is
// still per-user state under .git.
func (c *Cache) Save(path string) error {
	if c == nil || path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	c.mu.Lock()
	now := time.Now()
	entries := make(map[string]cacheEntry, len(c.entries))
	for k, e := range c.entries {
		if now.After(e.Expires) {
			continue
		}
		entries[k] = e
	}
	payload := cacheFile{Version: cacheFileVersion, Entries: entries}
	c.mu.Unlock()

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".trendradar-enrich-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
