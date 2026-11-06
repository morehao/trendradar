package releasewatch

import (
	"context"
	"testing"

	"github.com/morehao/trendradar/internal/fetch"
)

// relPre is rel() with prerelease=true, the shape of a rolling prerelease such
// as ragflow's "nightly".
func relPre(id int64, tag string) fetch.Release {
	r := rel(id, tag)
	r.Prerelease = true
	return r
}

// Repro of the ragflow incident: a schema-2 (core <= 0.10) cursor carries a
// numeric LastID but no tag memory, and nothing was ever published. The list
// head is a rolling prerelease whose release ID equals that watermark, so the
// legacy `r.ID <= LastID` fallback stopped the scan on row 0/1 forever and the
// repo reported "no change" — its first document could never be produced.
//
// The repair treats such a state as a first run: baseline + backfillLatest,
// which also replaces the poisoned cursor.
func TestBuildLegacyUnpublishedStateRebaselines(t *testing.T) {
	f := &fakeFetcher{releases: map[string][]fetch.Release{"o/r": {
		relPre(1, "nightly"), // head; release ID equals the legacy watermark
		rel(3, "v0.2.0"),
		rel(2, "v0.1.0"),
	}}}
	o := baseOpts(t, f)
	o.BaselineOnFirstRun = true
	o.BackfillLatest = 3
	o.IncludePrerelease = true

	st := NewState()
	// Exactly the legacy shape left behind by core 0.10: watermark at the rolling
	// head, empty tag memory, no published summary.
	st.Set("o/r", RepoState{LastID: 1, LastTag: "nightly", Status: StatusFailed})
	if err := st.Save(o.StatePath); err != nil {
		t.Fatal(err)
	}

	d, state, rep, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	// The same input used to yield d.Count == 0: the barrier stopped everything.
	if d.Count != 3 || rep.NewReleases != 3 || rep.Failed != 0 {
		t.Fatalf("count=%d new=%d failed=%d, want 3/3/0 (re-baseline + backfill)", d.Count, rep.NewReleases, rep.Failed)
	}
	got, _ := state.Get("o/r")
	if got.LastID != 0 {
		t.Fatalf("legacy watermark survived the re-baseline: LastID=%d", got.LastID)
	}
	// The re-baselined state is the same shape a fresh subscription gets: no ID
	// watermark, and the backfilled tags are already in the incremental cursor,
	// so the next run neither re-summarizes them nor hits the barrier again.
	for _, tag := range []string{"nightly", "v0.2.0", "v0.1.0"} {
		if !hasSummarized(got, tag) {
			t.Fatalf("%q missing from the tag cursor: %v", tag, got.SummarizedTags)
		}
	}
}

// A schema-2 cursor that did publish has no tag memory either, but its
// SeenTags are the closest thing to "already covered". Seed SummarizedTags from
// them and drop the ID watermark instead of trusting release IDs (a rolling
// tag at the head makes them non-monotonic over the list order).
func TestBuildLegacyPublishedStateMigratesToTags(t *testing.T) {
	f := &fakeFetcher{releases: map[string][]fetch.Release{"o/r": {rel(3, "v3"), rel(2, "v2"), rel(1, "v1")}}}
	o := baseOpts(t, f)

	st := NewState()
	st.Set("o/r", RepoState{
		LastID:          2,
		LastTag:         "v2",
		SeenTags:        []string{"v1", "v2"},
		LatestSummaryAt: "2026-11-01",
		Status:          StatusOK,
	})
	if err := st.Save(o.StatePath); err != nil {
		t.Fatal(err)
	}

	d, state, rep, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	if d.Count != 1 || rep.NewReleases != 1 {
		t.Fatalf("count=%d new=%d, want 1/1 (only v3)", d.Count, rep.NewReleases)
	}
	if tag := d.Repos[0].Releases[0].Tag; tag != "v3" {
		t.Fatalf("picked %q, want v3", tag)
	}
	got, _ := state.Get("o/r")
	if got.LastID != 3 {
		t.Fatalf("LastID=%d, want 3 (informational after the v3 cursor moves it)", got.LastID)
	}
	// Seen tags became the tag cursor, so the history is not re-summarized.
	for _, tag := range []string{"v1", "v2"} {
		if !hasSummarized(got, tag) {
			t.Fatalf("%q not migrated into SummarizedTags: %v", tag, got.SummarizedTags)
		}
	}
}

// Guard: once a state carries the tag cursor, the numeric ID must never gate
// the diff again — a rolling prerelease at the head can carry an ID far below
// the stale watermark, and a newer release must still be picked up.
func TestBuildV3CursorIgnoresStaleLastID(t *testing.T) {
	f := &fakeFetcher{releases: map[string][]fetch.Release{"o/r": {
		rel(5, "v1.0.0"), // newer than the rolling head below
		relPre(1, "nightly"),
	}}}
	o := baseOpts(t, f)
	o.IncludePrerelease = true

	st := NewState()
	st.Set("o/r", RepoState{
		LastID:          1_000_000, // stale numeric watermark, far above every ID here
		LastTag:         "nightly",
		SummarizedTags:  []string{"nightly"},
		LatestSummaryAt: "2026-11-01",
		Status:          StatusOK,
	})
	if err := st.Save(o.StatePath); err != nil {
		t.Fatal(err)
	}

	d, _, rep, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	if d.Count != 1 || rep.NewReleases != 1 {
		t.Fatalf("count=%d new=%d, want 1/1 (tag cursor only)", d.Count, rep.NewReleases)
	}
	if tag := d.Repos[0].Releases[0].Tag; tag != "v1.0.0" {
		t.Fatalf("picked %q, want v1.0.0", tag)
	}
}
