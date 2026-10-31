package summarize

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PromptVersion is bumped whenever SystemPrompt or the output contract changes,
// so cached summaries from an older prompt are not silently reused.
const PromptVersion = "v1"

// Cache stores summaries keyed by (namespace, fullName, sha256(description))
// with a TTL. It can be persisted under .git/ so a rerun reuses the same prose
// instead of re-asking the model -- which is what makes "same input produces the
// same digest" true.
type Cache struct {
	mu        sync.Mutex
	entries   map[string]cacheEntry
	ttl       time.Duration
	namespace string
}

type cacheEntry struct {
	Summary string    `json:"summary"`
	Expires time.Time `json:"expires"`
}

type cacheFile struct {
	Version int                   `json:"version"`
	Entries map[string]cacheEntry `json:"entries"`
}

const cacheFileVersion = 1

// NewCache returns an empty cache with a 30-day TTL.
func NewCache() *Cache {
	return &Cache{entries: make(map[string]cacheEntry), ttl: 30 * 24 * time.Hour}
}

// NamespaceFor scopes entries to a provider/model/prompt combination. Switching
// any of them misses the cache instead of reusing stale summaries.
func NamespaceFor(provider, model string) string {
	return provider + "|" + model + "|" + PromptVersion
}

// SetNamespace sets the cache namespace.
func (c *Cache) SetNamespace(namespace string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.namespace = namespace
}

// CachePath returns the on-disk cache location for a repository work tree.
func CachePath(repoDir string) string {
	return filepath.Join(repoDir, ".git", "trendradar-cache.json")
}

// LoadCache reads a persisted cache. A missing/empty path or a corrupt file
// yields a fresh empty cache: the cache is an optimization, never a source of
// truth, so damage must not fail a run.
func LoadCache(path string) (*Cache, error) {
	c := NewCache()
	if path == "" {
		return nil, nil
	}
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
		if e.Summary == "" || now.After(e.Expires) {
			continue
		}
		c.entries[k] = e
	}
	return c, nil
}

// Save writes the cache atomically, dropping expired entries first.
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
	tmp, err := os.CreateTemp(filepath.Dir(path), ".trendradar-cache-*")
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

func (c *Cache) key(fullName, desc string) string {
	h := sha256.Sum256([]byte(c.namespace + "\x00" + fullName + "\x00" + desc))
	return hex.EncodeToString(h[:])
}

// Get returns a cached summary if present and unexpired.
func (c *Cache) Get(fullName, desc string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[c.key(fullName, desc)]
	if !ok || time.Now().After(e.Expires) {
		return "", false
	}
	return e.Summary, true
}

// Put stores a summary.
func (c *Cache) Put(fullName, desc, summary string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[c.key(fullName, desc)] = cacheEntry{Summary: summary, Expires: time.Now().Add(c.ttl)}
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
