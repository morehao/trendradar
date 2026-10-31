package enrich

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/morehao/trendradar/internal/digest"
)

func repo(name string) digest.RepoSnapshot {
	return digest.RepoSnapshot{FullName: name, URL: "https://github.com/" + name}
}

const topicHTML = `<html><body>
<a class="topic-tag" href="/topics/llm">llm</a>
<a class="topic-tag" href="/topics/ai">ai</a>
<a href="/topics/ai">dup</a>
<script type="application/json">{"defaultBranch":"main"}</script>
</body></html>`

const readmeMD = `# Project One
[![Build](https://img.shields.io/badge/build-passing)](https://ci.example)
<b>Bold</b> introduction about widgets for teams.
![diagram](diagram.png)
[Website](https://example.com)
`

func TestParseMode(t *testing.T) {
	cases := map[string]Mode{"": ModeNone, "none": ModeNone, "repo": ModeRepo, "readme": ModeReadme, "README": ModeReadme}
	for in, want := range cases {
		got, err := ParseMode(in)
		if err != nil || got != want {
			t.Fatalf("ParseMode(%q) = %q,%v want %q", in, got, err, want)
		}
	}
	if _, err := ParseMode("bogus"); err == nil {
		t.Fatal("expected error for bogus mode")
	}
}

func TestEnrichRepoModeFetchesAndCaches(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(topicHTML))
	}))
	defer srv.Close()

	cache := NewCache()
	e := New(Options{Mode: ModeRepo, HTMLBase: srv.URL, RawBase: srv.URL, Budget: 2 * time.Second, Timeout: time.Second}, cache)
	repos := []digest.RepoSnapshot{repo("acme/one"), repo("acme/two")}

	out, rep := e.Enrich(context.Background(), repos)
	if len(out) != 2 || rep.Requested != 2 || rep.Fetched != 2 || rep.Failed != 0 || rep.Hit != 0 {
		t.Fatalf("first enrich: out=%d rep=%+v", len(out), rep)
	}
	if got := out["acme/one"].Topics; len(got) != 2 || got[0] != "ai" || got[1] != "llm" {
		t.Fatalf("topics = %v, want sorted [ai llm]", got)
	}
	if hits != 2 {
		t.Fatalf("http hits = %d, want 2", hits)
	}

	// Second call is served entirely from cache: no new HTTP traffic.
	out2, rep2 := e.Enrich(context.Background(), repos)
	if rep2.Hit != 2 || rep2.Fetched != 0 {
		t.Fatalf("second enrich rep = %+v, want hit=2 fetched=0", rep2)
	}
	if len(out2) != 2 {
		t.Fatalf("second enrich out = %d", len(out2))
	}
	if hits != 2 {
		t.Fatalf("cache miss caused extra http hits: %d", hits)
	}
}

func TestEnrichReadmeModeCleansAndTruncates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/README.md"):
			_, _ = w.Write([]byte(readmeMD))
		default:
			_, _ = w.Write([]byte(topicHTML))
		}
	}))
	defer srv.Close()

	e := New(Options{Mode: ModeReadme, ReadmeChars: 24, HTMLBase: srv.URL, RawBase: srv.URL, Budget: 3 * time.Second}, NewCache())
	out, rep := e.Enrich(context.Background(), []digest.RepoSnapshot{repo("acme/one")})
	if rep.Fetched != 1 {
		t.Fatalf("rep = %+v, want fetched=1", rep)
	}
	c := out["acme/one"]
	if len(c.Topics) == 0 {
		t.Fatal("topics missing in readme mode")
	}
	if c.ReadmeExcerpt == "" {
		t.Fatal("readme excerpt empty")
	}
	if n := utf8.RuneCountInString(c.ReadmeExcerpt); n > 24 {
		t.Fatalf("excerpt runes = %d, want <=24", n)
	}
	for _, bad := range []string{"shields.io", "<b>", "](", "![", "https://"} {
		if strings.Contains(c.ReadmeExcerpt, bad) {
			t.Fatalf("excerpt not cleaned, contains %q: %q", bad, c.ReadmeExcerpt)
		}
	}
}

func TestEnrich404Degrades(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	e := New(Options{Mode: ModeRepo, HTMLBase: srv.URL, RawBase: srv.URL, Budget: time.Second}, NewCache())
	out, rep := e.Enrich(context.Background(), []digest.RepoSnapshot{repo("acme/gone")})
	if len(out) != 0 || rep.Failed != 1 || rep.Fetched != 0 {
		t.Fatalf("404 should degrade: out=%d rep=%+v", len(out), rep)
	}
}

func TestEnrichRateLimitedStops(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	e := New(Options{Mode: ModeRepo, HTMLBase: srv.URL, RawBase: srv.URL, Concurrency: 1, Budget: 2 * time.Second}, NewCache())
	repos := []digest.RepoSnapshot{repo("a/1"), repo("a/2"), repo("a/3"), repo("a/4")}
	_, rep := e.Enrich(context.Background(), repos)
	if rep.Fetched != 0 {
		t.Fatalf("rate-limited run fetched %d", rep.Fetched)
	}
	if rep.RateLimited == 0 {
		t.Fatalf("expected rateLimited>0, rep=%+v", rep)
	}
}

func TestEnrichReadOnlySkips(t *testing.T) {
	e := New(Options{Mode: ModeRepo, ReadOnly: true, HTMLBase: "http://127.0.0.1:0", RawBase: "http://127.0.0.1:0"}, NewCache())
	_, rep := e.Enrich(context.Background(), []digest.RepoSnapshot{repo("a/1"), repo("a/2")})
	if rep.Skipped != 2 || rep.Fetched != 0 {
		t.Fatalf("read-only rep = %+v, want skipped=2", rep)
	}
}

func TestEnrichInvalidNameFails(t *testing.T) {
	e := New(Options{Mode: ModeRepo, HTMLBase: "http://127.0.0.1:0"}, NewCache())
	_, rep := e.Enrich(context.Background(), []digest.RepoSnapshot{{FullName: "not a repo"}})
	if rep.Failed != 1 {
		t.Fatalf("invalid name rep = %+v, want failed=1", rep)
	}
}

func TestCachePersists(t *testing.T) {
	path := CachePath(t.TempDir())
	c := NewCache()
	c.put("acme/one", ModeReadme, Context{Topics: []string{"go"}, ReadmeExcerpt: "hello"})
	if err := c.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := LoadCache(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, ok := loaded.get("acme/one", ModeReadme)
	if !ok || got.ReadmeExcerpt != "hello" || len(got.Topics) != 1 {
		t.Fatalf("reloaded context = %+v ok=%v", got, ok)
	}
	// Different mode must not hit the readme entry.
	if _, ok := loaded.get("acme/one", ModeRepo); ok {
		t.Fatal("repo mode must not reuse readme cache entry")
	}
}

func TestParseHelpers(t *testing.T) {
	if got := parseTopics([]byte(topicHTML)); fmt.Sprint(got) != "[ai llm]" {
		t.Fatalf("parseTopics = %v", got)
	}
	if got := parseDefaultBranch([]byte(topicHTML)); got != "main" {
		t.Fatalf("default branch = %q", got)
	}
	cleaned := cleanReadme(readmeMD)
	if strings.Contains(cleaned, "<") || strings.Contains(cleaned, "shields.io") {
		t.Fatalf("cleanReadme left markup: %q", cleaned)
	}
	if got := truncateRunes("你好世界", 2); got != "你好" {
		t.Fatalf("truncateRunes = %q", got)
	}
}
