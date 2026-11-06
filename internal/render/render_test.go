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
		Schema:          3,
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
				Summary:      "Go 官方仓库，今日热度上升。", SummarySource: "model",
			},
			{
				RepoSnapshot: digest.RepoSnapshot{FullName: "python/cpython", URL: "https://github.com/python/cpython", Description: digest.StrPtr("The Python programming language"), Language: "python", Stars: 64500, StarsInPeriod: 180, Forks: digest.IntPtr(30500), Rank: 2, Source: digest.SourceTrendingHTML},
				Summary:      "CPython 官方实现，关注度上升。", SummarySource: "model",
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
	d3.Repos[0].Summary = "different summary"
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

func indexLimits() map[string]int {
	return map[string]int{"daily": 90, "weekly": 12, "monthly": 12}
}

func TestBuildProducesThreeFiles(t *testing.T) {
	d := fixtureDigest()
	files, err := Build(d, []digest.Digest{d}, indexLimits(), "digests")
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
	for _, want := range []string{"digests/daily/2026-10/2026-10-30.md", "digests/daily/2026-10/2026-10-30.json", "digests/index.md"} {
		if !paths[want] {
			t.Fatalf("missing %s in %v", want, paths)
		}
	}
}

// Regression: output.dir must drive the rendered paths and the index links, so
// `git add <output.dir>` stages exactly what was written.
func TestBuildHonorsOutputDir(t *testing.T) {
	d := fixtureDigest()
	files, err := Build(d, []digest.Digest{d}, indexLimits(), "trending")
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
	for _, want := range []string{"trending/daily/2026-10/2026-10-30.md", "trending/daily/2026-10/2026-10-30.json", "trending/index.md"} {
		if !paths[want] {
			t.Fatalf("missing %s in %v", want, paths)
		}
	}
	if !strings.Contains(string(index), "(trending/daily/2026-10/2026-10-30.md)") {
		t.Fatalf("index link not rewritten to output dir:\n%s", index)
	}
}

func TestBuildWeeklyPath(t *testing.T) {
	d := fixtureDigest()
	d.Period = "weekly"
	d.Date = "2026-11-01" // Sunday -> ISO 2026-W44
	files, err := Build(d, []digest.Digest{d}, indexLimits(), "digests")
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, f := range files {
		paths[f.Path] = true
	}
	for _, want := range []string{"digests/weekly/2026/2026-W44.md", "digests/weekly/2026/2026-W44.json", "digests/index.md"} {
		if !paths[want] {
			t.Fatalf("missing %s in %v", want, paths)
		}
	}
}

func TestBuildMonthlyPath(t *testing.T) {
	d := fixtureDigest()
	d.Period = "monthly"
	d.Date = "2026-10-31"
	files, err := Build(d, []digest.Digest{d}, indexLimits(), "digests")
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, f := range files {
		paths[f.Path] = true
	}
	for _, want := range []string{"digests/monthly/2026/2026-10.md", "digests/monthly/2026/2026-10.json", "digests/index.md"} {
		if !paths[want] {
			t.Fatalf("missing %s in %v", want, paths)
		}
	}
}

func TestIndexSections(t *testing.T) {
	daily := []digest.Digest{
		{Date: "2026-10-30", Period: "daily", Count: 10},
		{Date: "2026-10-31", Period: "daily", Count: 11},
	}
	weekly := digest.Digest{Date: "2026-11-01", Period: "weekly", Count: 12}
	monthly := digest.Digest{Date: "2026-10-31", Period: "monthly", Count: 13}
	out := string(Index(append(append([]digest.Digest{}, daily...), weekly, monthly), indexLimits(), "digests"))

	for _, want := range []string{"## 日榜", "## 周榜", "## 月榜"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing section %q:\n%s", want, out)
		}
	}
	// daily newest first with the right leaf links.
	if !strings.Contains(out, "(digests/daily/2026-10/2026-10-31.md)") || !strings.Contains(out, "(digests/daily/2026-10/2026-10-30.md)") {
		t.Fatalf("daily links wrong:\n%s", out)
	}
	if !strings.Contains(out, "(digests/weekly/2026/2026-W44.md)") {
		t.Fatalf("weekly link wrong:\n%s", out)
	}
	if !strings.Contains(out, "(digests/monthly/2026/2026-10.md)") {
		t.Fatalf("monthly link wrong:\n%s", out)
	}
	// The newest daily (2026-10-31) must appear before the older one.
	if strings.Index(out, "2026-10-31.md") > strings.Index(out, "2026-10-30.md") {
		t.Fatalf("daily section not newest-first:\n%s", out)
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
	d.Repos[0].Summary = "[原文] The Go programming language | with a pipe"
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
	if m["schema"].(float64) != 3 {
		t.Fatalf("schema = %v, want 3", m["schema"])
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

// perPageDigest builds a digest with three repos whose own languages differ from
// the pages they were scraped from, so the sectioning test can prove sections
// follow pages (not repo languages).
func perPageDigest() (digest.Digest, map[string][]digest.RepoDigest) {
	rd := func(name, repoLang string, rank int, summary string) digest.RepoDigest {
		return digest.RepoDigest{
			RepoSnapshot: digest.RepoSnapshot{FullName: name, URL: "https://github.com/" + name, Language: repoLang, Stars: 100, StarsInPeriod: 10, Rank: rank, Source: digest.SourceTrendingHTML},
			Summary:      summary, SummarySource: "model",
		}
	}
	// Repo own languages: the C++ and Go repos came from the global "all" page;
	// the Python repo came from the python page.
	allA := rd("acme/hypr", "c++", 1, "全局榜里的 C++ 仓库")
	allB := rd("acme/gocli", "go", 2, "全局榜里的 Go 仓库")
	py := rd("acme/reqs", "python", 1, "python 页的仓库")

	d := digest.Digest{
		Schema:      3,
		Date:        "2026-11-03",
		Period:      "daily",
		Languages:   []string{"all", "python", "rust"},
		Count:       3,
		Degraded:    false,
		CoreVersion: digest.StrPtr("0.8.4"),
	}
	d.Repos = []digest.RepoDigest{allA, allB, py}
	d.ContentHash = ContentHash(d)

	byPage := map[string][]digest.RepoDigest{
		"all":    {allA, allB},
		"python": {py},
		"rust":   {},
	}
	return d, byPage
}

// Sections must follow the requested pages: one "## all", one "## python", one
// empty "## rust"; the all page stays whole (no "## c++" / "## go" split), and
// the languages front matter echoes the requested list verbatim.
func TestMarkdownSectionsFollowRequestedPages(t *testing.T) {
	d, byPage := perPageDigest()
	out := string(MarkdownWithBanners(d, byPage, nil))

	for _, want := range []string{"## all", "## python", "## rust"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing section %q:\n%s", want, out)
		}
	}
	// all page stays consolidated: no per-repo-language sections.
	for _, bad := range []string{"## c++", "## go"} {
		if strings.Contains(out, bad) {
			t.Fatalf("unexpected per-repo-language section %q:\n%s", bad, out)
		}
	}
	// empty rust section must still be present (header, empty table).
	rustIdx := strings.Index(out, "## rust")
	if rustIdx < 0 {
		t.Fatalf("rust section missing:\n%s", out)
	}
	if strings.Contains(out[rustIdx:], "| [") {
		t.Fatalf("rust section should be empty:\n%s", out[rustIdx:])
	}
	// front matter echoes requested languages.
	if !strings.Contains(out, "languages: [all, python, rust]") {
		t.Fatalf("front matter not echoing requested languages:\n%s", out)
	}
	// all-page repos appear under ## all (verify both names present before python).
	allIdx := strings.Index(out, "## all")
	pyIdx := strings.Index(out, "## python")
	if allIdx < 0 || pyIdx < 0 || !(allIdx < pyIdx) {
		t.Fatalf("section order wrong:\n%s", out)
	}
	allBody := out[allIdx:pyIdx]
	if !strings.Contains(allBody, "acme/hypr") || !strings.Contains(allBody, "acme/gocli") {
		t.Fatalf("all section missing its repos:\n%s", allBody)
	}
}

// A repo present on both the "all" page and a language page must show up under
// both sections (cross-page duplication is intentional per the design).
func TestMarkdownAllowsCrossPageDuplication(t *testing.T) {
	d, byPage := perPageDigest()
	dup := byPage["all"][1] // acme/gocli, also pretend it trended on python
	byPage["python"] = append(byPage["python"], dup)
	out := string(MarkdownWithBanners(d, byPage, nil))

	allIdx := strings.Index(out, "## all")
	pyIdx := strings.Index(out, "## python")
	rustIdx := strings.Index(out, "## rust")
	if allIdx < 0 || pyIdx < 0 || rustIdx < 0 {
		t.Fatalf("missing sections:\n%s", out)
	}
	if !strings.Contains(out[allIdx:pyIdx], "acme/gocli") || !strings.Contains(out[pyIdx:rustIdx], "acme/gocli") {
		t.Fatalf("duplicated repo should appear in both all and python sections:\n%s", out)
	}
}

// A nil byPage (replay / search fallback) keeps the legacy per-repo-language
// layout, and sections still order with requested languages first.
func TestMarkdownNilByPageFallsBackToRepoLanguage(t *testing.T) {
	d, _ := perPageDigest()
	out := string(MarkdownWithBanners(d, nil, nil))
	// Legacy layout groups by repo language => c++ and go sections exist.
	for _, want := range []string{"## c++", "## go", "## python"} {
		if !strings.Contains(out, want) {
			t.Fatalf("legacy layout missing %q:\n%s", want, out)
		}
	}
}

// TestMarkdownEnGolden renders the same fixture in English; the prose summaries
// are replaced by the fixture's English text and the fixed strings come from the
// en RenderTexts.
func enFixtureDigest() digest.Digest {
	d := fixtureDigest()
	d.Repos[0].Summary = "The official Go repository; trending up today."
	d.Repos[1].Summary = "The official CPython implementation; gaining attention."
	d.ContentHash = ContentHash(d)
	return d
}

func TestMarkdownEnGolden(t *testing.T) {
	d := enFixtureDigest()
	got := MarkdownLang(d, "en")
	goldenPath := filepath.Join("testdata", "digest.en.md")
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
		t.Fatalf("en markdown mismatch\ngot:\n%s\nwant:\n%s", got, string(want))
	}
	// The en fixed strings must be present (title + table header).
	if !strings.Contains(string(got), "Trending Digest") || !strings.Contains(string(got), "| Repository | Stars | Stars today | Summary |") {
		t.Fatalf("en fixed strings missing:\n%s", got)
	}
}
