package releasewatch

import (
	"context"
	"testing"
	"time"

	"github.com/morehao/trendradar/internal/fetch"
)

// relAt builds a release with an explicit publish date; empty published means a
// release GitHub did not date.
func relAt(id int64, tag, published string, prerelease bool) fetch.Release {
	r := fetch.Release{
		ID: id, TagName: tag, Name: tag, Body: "notes " + tag,
		Prerelease: prerelease,
		HTMLURL:    "https://github.com/o/r/releases/tag/" + tag,
	}
	if published != "" {
		ts, err := time.Parse(time.RFC3339, published)
		if err != nil {
			panic(err)
		}
		r.PublishedAt = &ts
	}
	return r
}

// Regression: backfill must pick the newest N versions by publication date, not
// by the API's list position. GitHub orders /releases by created_at, so a
// rolling prerelease (ragflow's "nightly": fresh created_at, publishedAt from a
// year ago) sits at the head; taken positionally it would displace a real
// version and pull the document's newest week back to that stale date.
func TestBuildBackfillPicksNewestByPublishDate(t *testing.T) {
	f := &fakeFetcher{releases: map[string][]fetch.Release{"o/r": {
		relAt(1, "nightly", "2025-12-01T13:17:42Z", true), // head position, year-old date
		relAt(4, "v1.1.0", "2026-11-06T12:31:31Z", false),
		relAt(3, "v1.0.0-rc3", "2026-10-30T11:53:15Z", false),
		relAt(2, "v1.0.0-rc2", "2026-10-16T09:49:48Z", false),
		relAt(0, "v0.9.0", "", false), // undated: must sort last
	}}}
	o := baseOpts(t, f)
	o.BaselineOnFirstRun = true
	o.BackfillLatest = 3
	o.IncludePrerelease = true

	d, state, rep, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	if d.Count != 3 || rep.NewReleases != 3 {
		t.Fatalf("count=%d new=%d, want 3/3", d.Count, rep.NewReleases)
	}
	var tags []string
	for _, item := range d.Repos[0].Releases {
		tags = append(tags, item.Tag)
	}
	want := []string{"v1.1.0", "v1.0.0-rc3", "v1.0.0-rc2"}
	if len(tags) != len(want) {
		t.Fatalf("backfilled tags=%v, want %v", tags, want)
	}
	for i := range want {
		if tags[i] != want[i] {
			t.Fatalf("backfilled tags=%v, want %v", tags, want)
		}
	}
	got, _ := state.Get("o/r")
	for _, tag := range want {
		if !hasSummarized(got, tag) {
			t.Fatalf("%q missing from the cursor after backfill: %v", tag, got.SummarizedTags)
		}
	}
	if hasSummarized(got, "nightly") {
		t.Fatalf("rolling prerelease must not be backfilled: %v", got.SummarizedTags)
	}
}
