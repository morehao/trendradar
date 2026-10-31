package render

import (
	"strings"
	"testing"

	"github.com/morehao/trendradar/internal/releasedigest"
)

func sampleRelease(tag string) releasedigest.ReleaseDigest {
	return releasedigest.ReleaseDigest{
		Schema: 1, Date: "2026-10-31", Period: "daily", Count: 1,
		Repos: []releasedigest.RepoReleases{{
			FullName: "o/r", URL: "https://github.com/o/r", LatestTag: tag, NewCount: 1,
			Releases: []releasedigest.ReleaseItem{{Tag: tag, Name: tag, Kind: "release", SummaryZh: "摘要", SummarySource: "model"}},
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
