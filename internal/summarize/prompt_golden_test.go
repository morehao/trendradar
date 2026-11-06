package summarize

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/i18n"
)

var updatePromptGolden = flag.Bool("update", false, "rewrite prompt golden files")

// promptGoldenBatch is a fixed batch so the zh/en prompt snapshots are stable.
func promptGoldenBatch() ([]digest.RepoSnapshot, map[string]Context) {
	batch := []digest.RepoSnapshot{
		{FullName: "acme/cache", Language: "go", Stars: 1234, StarsInPeriod: 56, Rank: 3,
			Description: digest.StrPtr("fast cache\nfor services")},
		{FullName: "acme/ui", Language: "typescript", Stars: 900, StarsInPeriod: 12, Rank: 7,
			Description: digest.StrPtr("ui components")},
	}
	ctxs := map[string]Context{
		"acme/cache": {Topics: []string{"ai", "llm"}, ReadmeExcerpt: "line one\nline two"},
	}
	return batch, ctxs
}

func checkPromptGolden(t *testing.T, name string, got string) {
	t.Helper()
	path := filepath.Join("testdata", "prompt", name)
	if *updatePromptGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (use -update): %v", name, err)
	}
	if !bytes.Equal([]byte(got), want) {
		t.Fatalf("%s mismatch\ngot:\n%s\nwant:\n%s", name, got, string(want))
	}
}

func TestPromptGoldenZh(t *testing.T) {
	lang := i18n.MustResolve("zh")
	batch, ctxs := promptGoldenBatch()
	checkPromptGolden(t, "system.zh.txt", systemPromptFor(lang))
	checkPromptGolden(t, "user.zh.txt", userPromptFor(lang, batch, ctxs))
}

func TestPromptGoldenEn(t *testing.T) {
	lang := i18n.MustResolve("en")
	batch, ctxs := promptGoldenBatch()
	checkPromptGolden(t, "system.en.txt", systemPromptFor(lang))
	checkPromptGolden(t, "user.en.txt", userPromptFor(lang, batch, ctxs))
}

// TestSystemPromptMatchesZhRegistry guards the exported SystemPrompt var against
// drifting from the zh registry entry.
func TestSystemPromptMatchesZhRegistry(t *testing.T) {
	if SystemPrompt != i18n.MustResolve("zh").TrendingSystem {
		t.Fatal("SystemPrompt must equal the zh registry TrendingSystem")
	}
}

// TestPromptOutputKeyIsSummary verifies both language prompts ask for the
// renamed output key (summary, not summaryZh).
func TestPromptOutputKeyIsSummary(t *testing.T) {
	for _, id := range i18n.Supported() {
		lang := i18n.MustResolve(id)
		if bytes.Contains([]byte(lang.TrendingSystem), []byte("summaryZh")) {
			t.Errorf("%s trending prompt still references summaryZh", id)
		}
		if !bytes.Contains([]byte(lang.TrendingSystem), []byte(`"summary"`)) {
			t.Errorf("%s trending prompt does not reference the summary key", id)
		}
	}
}
