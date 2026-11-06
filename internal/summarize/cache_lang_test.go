package summarize

import (
	"context"
	"sync"
	"testing"

	"github.com/morehao/trendradar/internal/i18n"
	"github.com/morehao/trendradar/internal/model"
)

// langCountingProvider records the system prompt it was called with so a test
// can assert which language the prompt used.
type langCountingProvider struct {
	inner   model.Provider
	mu      sync.Mutex
	calls   int
	systems []string
}

func (p *langCountingProvider) Name() string { return "lang-counting" }
func (p *langCountingProvider) Complete(ctx context.Context, s, u string) (model.Result, error) {
	p.mu.Lock()
	p.calls++
	p.systems = append(p.systems, s)
	p.mu.Unlock()
	return p.inner.Complete(ctx, s, u)
}

func TestNamespaceIncludesLanguage(t *testing.T) {
	zh := NamespaceForLang("p", "m", "none", "zh")
	en := NamespaceForLang("p", "m", "none", "en")
	if zh == en {
		t.Fatalf("zh and en namespaces must differ: %q", zh)
	}
	// Empty lang normalizes to zh so the default namespace stays stable.
	if NamespaceForLang("p", "m", "none", "") != zh {
		t.Fatal("empty lang namespace should equal zh namespace")
	}
	// PromptVersion must be present and bumped to v3.
	if got := PromptVersion; got != "v3" {
		t.Fatalf("PromptVersion = %q, want v3", got)
	}
}

// TestCacheMissesOnLanguageSwitch proves a summary cached under zh is never
// reused for an en run: switching the language namespace forces a fresh model
// call (and the second run is served from its own cache afterwards).
func TestCacheMissesOnLanguageSwitch(t *testing.T) {
	cp := &langCountingProvider{inner: model.NewFake()}
	cache := NewCache()
	repos := snaps(3)

	// zh run: one call, cache populated under the zh namespace.
	cache.SetNamespace(NamespaceForLang("p", "m", "none", "zh"))
	sZh := New(cp, WithCache(cache), WithLanguage("zh"))
	sZh.Summarize(context.Background(), repos)
	if cp.calls != 1 {
		t.Fatalf("zh first call count = %d, want 1", cp.calls)
	}
	if got := cp.systems[0]; got != i18n.MustResolve("zh").TrendingSystem {
		t.Fatal("zh run should send the zh system prompt")
	}

	// en run with the same cache object but en namespace: cache miss -> new call.
	cache.SetNamespace(NamespaceForLang("p", "m", "none", "en"))
	sEn := New(cp, WithCache(cache), WithLanguage("en"))
	out := sEn.Summarize(context.Background(), repos)
	if cp.calls != 2 {
		t.Fatalf("en run should miss zh cache and call once; calls = %d, want 2", cp.calls)
	}
	if got := cp.systems[1]; got != i18n.MustResolve("en").TrendingSystem {
		t.Fatal("en run should send the en system prompt")
	}
	for _, d := range out {
		if d.SummaryLang != "en" {
			t.Fatalf("%s SummaryLang = %q, want en", d.FullName, d.SummaryLang)
		}
	}

	// Second en run hits the en cache: no new call.
	sEn2 := New(cp, WithCache(cache), WithLanguage("en"))
	sEn2.Summarize(context.Background(), repos)
	if cp.calls != 2 {
		t.Fatalf("second en run should hit cache; calls = %d, want still 2", cp.calls)
	}
}

// TestSummarizeTagsLanguage checks every produced digest carries the language id,
// including the degrade path.
func TestSummarizeTagsLanguage(t *testing.T) {
	s := New(badProvider{}, WithMaxRetries(0), WithLanguage("en"))
	out := s.Summarize(context.Background(), snaps(2))
	for _, d := range out {
		if d.SummaryLang != "en" {
			t.Fatalf("%s SummaryLang = %q, want en", d.FullName, d.SummaryLang)
		}
		if d.SummarySource != "original" {
			t.Fatalf("%s should degrade", d.FullName)
		}
	}
}
