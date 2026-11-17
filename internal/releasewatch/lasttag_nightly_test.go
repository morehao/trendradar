package releasewatch

import (
	"context"
	"testing"

	"github.com/morehao/trendradar/internal/fetch"
)

// Regression: the repo's surfaced "latest version" (cursor LastTag and the
// document's LatestTag) must be the newest by publish date, not the /releases
// API head. ragflow's rolling "nightly" (fresh created_at, year-old publishedAt,
// prerelease) sits at the head; positional selection would report it as latest
// even though v1.1.0 was published far more recently.
func TestLastTagIgnoresRollingPrereleaseAtHead(t *testing.T) {
	f := &fakeFetcher{releases: map[string][]fetch.Release{"o/r": {
		relAt(1, "nightly", "2025-12-01T13:17:42Z", true), // head, year-old date
		relAt(4, "v1.1.0", "2026-11-06T12:31:31Z", false),
		relAt(3, "v1.0.0-rc3", "2026-10-30T11:53:15Z", false),
		relAt(2, "v1.0.0-rc2", "2026-10-16T09:49:48Z", false),
	}}}

	// Baseline path (first subscription + backfill).
	o := baseOpts(t, f)
	o.BaselineOnFirstRun = true
	o.BackfillLatest = 3
	o.IncludePrerelease = true
	d, state, _, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := state.Get("o/r")
	if got.LastTag != "v1.1.0" {
		t.Fatalf("baseline LastTag=%q, want v1.1.0 (newest by publish date)", got.LastTag)
	}
	if d.Repos[0].LatestTag != "v1.1.0" {
		t.Fatalf("doc LatestTag=%q, want v1.1.0", d.Repos[0].LatestTag)
	}

	// Regular path: a later week picks up a brand-new release; LastTag must move
	// to it, not stay pinned to the rolling head.
	f.releases["o/r"] = []fetch.Release{
		relAt(1, "nightly", "2025-12-01T13:17:42Z", true),
		relAt(6, "v1.2.0", "2026-11-13T10:00:00Z", false),
		relAt(4, "v1.1.0", "2026-11-06T12:31:31Z", false),
	}
	o2 := baseOpts(t, f)
	o2.IncludePrerelease = true
	d2, state2, _, err := Build(context.Background(), f, o2)
	if err != nil {
		t.Fatal(err)
	}
	got2, _ := state2.Get("o/r")
	if got2.LastTag != "v1.2.0" {
		t.Fatalf("regular LastTag=%q, want v1.2.0", got2.LastTag)
	}
	if d2.Repos[0].LatestTag != "v1.2.0" {
		t.Fatalf("doc2 LatestTag=%q, want v1.2.0", d2.Repos[0].LatestTag)
	}
}

// Undated candidates (tag fallback) must never displace a dated one as "latest".
func TestNewestByPublishDateSkipsUndated(t *testing.T) {
	cands := []candidate{
		{rel: fetch.Release{TagName: "nightly"}}, // no date
		{rel: relAt(2, "v1.0.0", "2026-10-01T00:00:00Z", false)},
	}
	got := newestByPublishDate(cands)
	if got == nil || got.tagName() != "v1.0.0" {
		t.Fatalf("newestByPublishDate=%v, want v1.0.0", got)
	}
	if newestByPublishDate([]candidate{{rel: fetch.Release{TagName: "x"}}}) != nil {
		t.Fatal("all-undated batch must yield nil")
	}
}
