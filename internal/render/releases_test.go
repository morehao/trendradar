package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morehao/trendradar/internal/releasedigest"
)

func sampleRelease(tag string) releasedigest.ReleaseDigest {
	return releasedigest.ReleaseDigest{
		Schema: 2, Date: "2026-10-31", Period: "daily", Count: 1,
		Repos: []releasedigest.RepoReleases{{
			FullName: "o/r", URL: "https://github.com/o/r", LatestTag: tag, NewCount: 1,
			Releases: []releasedigest.ReleaseItem{{Tag: tag, Name: tag, Kind: "release", SummarySource: "model"}},
		}},
	}
}

func TestBuildReleasesPaths(t *testing.T) {
	d := sampleRelease("v1")
	files, err := BuildReleases(d, []releasedigest.ReleaseDigest{d}, map[string]int{"daily": 90}, "digests", "releases")
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, f := range files {
		paths[f.Path] = true
	}
	for _, want := range []string{
		"digests/releases/daily/2026-10/2026-10-31.md",
		"digests/releases/daily/2026-10/2026-10-31.json",
		"digests/releases/index.md",
	} {
		if !paths[want] {
			t.Fatalf("missing %s in %v", want, paths)
		}
	}
	var md string
	for _, f := range files {
		if strings.HasSuffix(f.Path, "2026-10-31.md") {
			md = string(f.Data)
		}
	}
	if !strings.Contains(md, "v1") || !strings.Contains(md, "发版摘要") {
		t.Fatalf("markdown missing content:\n%s", md)
	}
}

func TestMergeReleaseDigestsSameDay(t *testing.T) {
	a := sampleRelease("v1")
	b := sampleRelease("v2")
	list, today := MergeReleaseDigests([]releasedigest.ReleaseDigest{a}, b)
	if len(list) != 1 {
		t.Fatalf("list len=%d, want 1", len(list))
	}
	if today.Count != 2 {
		t.Fatalf("count=%d, want 2", today.Count)
	}
}

// Weekly digests live under "_weekly" so the directory cannot collide with a
// subscribed owner named "weekly".
func TestBuildWeeklyReleasesPath(t *testing.T) {
	d := sampleRelease("v1")
	d.Period = "weekly"
	d.Date = "2026-11-01"
	files, err := BuildReleases(d, []releasedigest.ReleaseDigest{d}, map[string]int{"weekly": 12}, "digests", "releases")
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{}
	for _, f := range files {
		paths[f.Path] = string(f.Data)
	}
	for _, want := range []string{
		"digests/releases/_weekly/2026/2026-W44.md",
		"digests/releases/_weekly/2026/2026-W44.json",
		"digests/releases/index.md",
	} {
		if _, ok := paths[want]; !ok {
			t.Fatalf("missing %s in %v", want, paths)
		}
	}
	if idx := paths["digests/releases/index.md"]; !strings.Contains(idx, "_weekly/2026/2026-W44.md") {
		t.Fatalf("index must link the underscored path:\n%s", idx)
	}
}

// Reading must scan the current directory and the pre-0.7 "weekly" one, keeping
// one document per week.
func TestListReleaseDigestsReadsBothWeeklyDirs(t *testing.T) {
	root := t.TempDir()
	write := func(dir, name, date string) {
		p := filepath.Join(root, "releases", dir, "2026", name+".json")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"schema":2,"date":"` + date + `","period":"weekly","contentHash":"sha256:` +
			strings.Repeat("d", 64) + `","contentHashAlgo":"jcs-sha256-v1","count":0,"degraded":false,"repos":[]}`
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("_weekly", "2026-W44", "2026-11-01")
	write("weekly", "2026-W43", "2026-10-25")
	write("weekly", "2026-W44", "2026-11-01") // half-migrated duplicate

	ds, err := ListReleaseDigests(root, "releases", map[string]int{"weekly": 12})
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 2 {
		t.Fatalf("digests=%d, want 2 (deduped by week)", len(ds))
	}
	if ds[0].Date != "2026-11-01" || ds[1].Date != "2026-10-25" {
		t.Fatalf("order=%s,%s; want newest first", ds[0].Date, ds[1].Date)
	}
}
