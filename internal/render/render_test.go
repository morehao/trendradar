package render

import (
	"bytes"
	"encoding/json"
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
		Schema:          2,
		Date:            "2026-10-30",
		Period:          "daily",
		Languages:       []string{"all", "go", "python"},
		Count:           2,
		Degraded:        false,
		CoreVersion:     digest.StrPtr("0.1.0"),
		GeneratedAt:     tptr(time.Date(2026, 10, 30, 0, 5, 0, 0, time.UTC)),
		ContentHashAlgo: "jcs-sha256-v1",
		Repos: []digest.RepoDigest{
			{
				RepoSnapshot: digest.RepoSnapshot{FullName: "golang/go", URL: "https://github.com/golang/go", Description: digest.StrPtr("The Go programming language"), Language: "go", Stars: 126000, StarsInPeriod: 320, Forks: digest.IntPtr(17800), Rank: 1, Source: digest.SourceTrendingHTML},
				SummaryZh:    "Go 官方仓库，今日热度上升。", SummarySource: "model",
			},
			{
				RepoSnapshot: digest.RepoSnapshot{FullName: "python/cpython", URL: "https://github.com/python/cpython", Description: digest.StrPtr("The Python programming language"), Language: "python", Stars: 64500, StarsInPeriod: 180, Forks: digest.IntPtr(30500), Rank: 2, Source: digest.SourceTrendingHTML},
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
	ng := d1.GeneratedAt.Add(3 * time.Hour)
	d2.GeneratedAt = &ng
	d2.CoreVersion = digest.StrPtr("9.9.9")
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

func TestJSONV2ShapeAndOptionalOmission(t *testing.T) {
	d := fixtureDigest()
	b, err := JSON(d)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["schema"].(float64) != 2 {
		t.Fatalf("schema = %v, want 2", m["schema"])
	}
	if m["contentHashAlgo"] != "jcs-sha256-v1" {
		t.Fatalf("contentHashAlgo = %v", m["contentHashAlgo"])
	}
	// optional fields present when set
	repo0 := m["repos"].([]any)[0].(map[string]any)
	if _, ok := repo0["description"]; !ok {
		t.Fatal("description should be present")
	}
	if _, ok := repo0["forks"]; !ok {
		t.Fatal("forks should be present")
	}
	// omitted when absent (nil pointers + omitempty)
	d2 := fixtureDigest()
	d2.Repos[0].Description = nil
	d2.Repos[0].Forks = nil
	b2, _ := JSON(d2)
	var m2 map[string]any
	_ = json.Unmarshal(b2, &m2)
	r0 := m2["repos"].([]any)[0].(map[string]any)
	if _, ok := r0["description"]; ok {
		t.Fatal("description should be omitted when empty")
	}
	if _, ok := r0["forks"]; ok {
		t.Fatal("forks should be omitted when zero")
	}
}

func tptr(t time.Time) *time.Time { return &t }

// Two renders of the same digest differ only by generatedAt; idempotency must
// treat them as the same artifact.
func TestComparableIgnoresGeneratedAt(t *testing.T) {
	base := fixtureDigest()
	d2 := base
	gen := base.GeneratedAt.Add(3 * time.Hour)
	d2.GeneratedAt = &gen

	md1, md2 := Markdown(base), Markdown(d2)
	if bytes.Equal(md1, md2) {
		t.Fatal("fixture should differ before normalization")
	}
	if !bytes.Equal(Comparable("2026-10-30.md", md1), Comparable("2026-10-30.md", md2)) {
		t.Fatal("markdown comparable bytes must ignore generatedAt")
	}

	js1, err := JSON(base)
	if err != nil {
		t.Fatal(err)
	}
	js2, err := JSON(d2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(Comparable("2026-10-30.json", js1), Comparable("2026-10-30.json", js2)) {
		t.Fatal("json comparable bytes must ignore generatedAt")
	}
}
