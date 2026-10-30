package render

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morehao/trendradar/internal/digest"
)

var update = flag.Bool("update", false, "rewrite golden files")

func fixtureDigest() digest.Digest {
	d := digest.Digest{
		Schema:      1,
		Date:        "2026-10-30",
		Period:      "daily",
		Languages:   []string{"all", "go", "python"},
		Count:       2,
		Degraded:    false,
		CoreVersion: "0.1.0",
		GeneratedAt: time.Date(2026, 10, 30, 0, 5, 0, 0, time.UTC),
		Repos: []digest.RepoDigest{
			{
				RepoSnapshot: digest.RepoSnapshot{FullName: "golang/go", URL: "https://github.com/golang/go", Description: "The Go programming language", Language: "go", Stars: 126000, StarsInPeriod: 320, Forks: 17800, Rank: 1, Source: digest.SourceTrendingHTML},
				SummaryZh:    "Go 官方仓库，今日热度上升。", SummarySource: "model",
			},
			{
				RepoSnapshot: digest.RepoSnapshot{FullName: "python/cpython", URL: "https://github.com/python/cpython", Description: "The Python programming language", Language: "python", Stars: 64500, StarsInPeriod: 180, Forks: 30500, Rank: 2, Source: digest.SourceTrendingHTML},
				SummaryZh:    "CPython 官方实现，关注度上升。", SummarySource: "model",
			},
		},
	}
	d.ContentHash = ContentHash(d)
	return d
}

func TestContentHashStable(t *testing.T) {
	d1 := fixtureDigest()
	d2 := fixtureDigest()
	d2.GeneratedAt = d1.GeneratedAt.Add(3 * time.Hour)
	d2.CoreVersion = "9.9.9"
	if ContentHash(d1) != ContentHash(d2) {
		t.Fatal("hash should ignore generatedAt and coreVersion")
	}
	d3 := fixtureDigest()
	d3.Repos[0].SummaryZh = "different summary"
	if ContentHash(d1) == ContentHash(d3) {
		t.Fatal("hash should change when repo summary changes")
	}
	if len(d1.ContentHash) != len("sha256:")+64 {
		t.Fatalf("bad hash format: %s", d1.ContentHash)
	}
}

func TestMarkdownGolden(t *testing.T) {
	d := fixtureDigest()
	got := Markdown(d)
	goldenPath := filepath.Join("testdata", "digest.md")
	if *update {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (use -update): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("markdown mismatch\ngot:\n%s", got)
	}
}

func TestBuildProducesThreeFiles(t *testing.T) {
	d := fixtureDigest()
	files, err := Build(d, []digest.Digest{d}, 90, "digests")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("files = %d, want 3", len(files))
	}
	paths := map[string]bool{}
	for _, f := range files {
		paths[f.Path] = true
	}
	for _, want := range []string{"digests/2026-10-30/2026-10-30.md", "digests/2026-10-30/2026-10-30.json", "digests/index.md"} {
		if !paths[want] {
			t.Fatalf("missing %s in %v", want, paths)
		}
	}
}

// Regression: output.dir must drive the rendered paths and the index links, so
// `git add <output.dir>` stages exactly what was written.
func TestBuildHonorsOutputDir(t *testing.T) {
	d := fixtureDigest()
	files, err := Build(d, []digest.Digest{d}, 90, "trending")
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	var index []byte
	for _, f := range files {
		paths[f.Path] = true
		if strings.HasSuffix(f.Path, "index.md") {
			index = f.Data
		}
	}
	for _, want := range []string{"trending/2026-10-30/2026-10-30.md", "trending/2026-10-30/2026-10-30.json", "trending/index.md"} {
		if !paths[want] {
			t.Fatalf("missing %s in %v", want, paths)
		}
	}
	if !strings.Contains(string(index), "(trending/2026-10-30/2026-10-30.md)") {
		t.Fatalf("index link not rewritten to output dir:\n%s", index)
	}
}

func TestOutputRootSanitizes(t *testing.T) {
	cases := map[string]string{
		"":          "digests",
		".":         "digests",
		"  ":        "digests",
		"/abs/path": "digests",
		"../escape": "digests",
		"digests":   "digests",
		"trending":  "trending",
		"a/b":       "a/b",
	}
	for in, want := range cases {
		if got := OutputRoot(in); got != want {
			t.Errorf("OutputRoot(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMarkdownTableAndDegradedNotice(t *testing.T) {
	d := fixtureDigest()
	d.Degraded = true
	d.Repos[0].SummarySource = "original"
	d.Repos[0].SummaryZh = "[原文] The Go programming language | with a pipe"
	md := string(Markdown(d))
	if !strings.Contains(md, "降级") {
		t.Fatalf("degraded notice missing:\n%s", md)
	}
	if !strings.Contains(md, "| 仓库 | Stars | 今日新增 | 摘要 |") {
		t.Fatalf("table header missing:\n%s", md)
	}
	if !strings.Contains(md, "\\|") {
		t.Fatalf("pipe in a cell must be escaped:\n%s", md)
	}
	if strings.Contains(md, " ★") {
		t.Fatalf("old bullet format should be gone:\n%s", md)
	}
}
