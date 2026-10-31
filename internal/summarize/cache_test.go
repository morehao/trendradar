package summarize

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCachePersistsAcrossRuns(t *testing.T) {
	path := CachePath(t.TempDir())
	c := NewCache()
	c.SetNamespace(NamespaceFor("openai-compatible", "m1"))
	c.Put("acme/one", "desc", "summary")
	if err := c.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cache file missing: %v", err)
	}

	loaded, err := LoadCache(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded == nil {
		t.Fatal("loaded cache is nil")
	}
	loaded.SetNamespace(NamespaceFor("openai-compatible", "m1"))
	if got, ok := loaded.Get("acme/one", "desc"); !ok || got != "summary" {
		t.Fatalf("cache miss after reload: %q, %v", got, ok)
	}
}

func TestCacheNamespaceMisses(t *testing.T) {
	path := CachePath(t.TempDir())
	c := NewCache()
	c.SetNamespace(NamespaceFor("openai-compatible", "m1"))
	c.Put("acme/one", "desc", "summary")
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}

	loaded, _ := LoadCache(path)
	loaded.SetNamespace(NamespaceFor("openai-compatible", "m2"))
	if _, ok := loaded.Get("acme/one", "desc"); ok {
		t.Fatal("a different model must not reuse cached summaries")
	}
}

func TestLoadCacheToleratesMissingAndCorrupt(t *testing.T) {
	if c, err := LoadCache(""); err != nil || c != nil {
		t.Fatalf("empty path: %v, %v", c, err)
	}
	path := CachePath(t.TempDir())
	if c, err := LoadCache(path); err != nil || c == nil {
		t.Fatalf("missing file must yield an empty cache: %v, %v", c, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := LoadCache(path); err != nil || c == nil {
		t.Fatalf("corrupt file must yield an empty cache: %v, %v", c, err)
	}
}
