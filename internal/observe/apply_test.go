package observe

import (
	"context"
	"errors"
	"testing"

	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/fetch"
)

// fakeFetcher implements Fetcher for tests without network access.
type fakeFetcher struct {
	stars    map[string]fetch.StarCount
	starsErr error
	history  map[string][]fetch.StarHistoryPoint
	histErr  map[string]error
}

func (f fakeFetcher) FetchStars(_ context.Context, repos []string) (map[string]fetch.StarCount, error) {
	if f.starsErr != nil {
		return nil, f.starsErr
	}
	out := make(map[string]fetch.StarCount, len(repos))
	for _, r := range repos {
		if sc, ok := f.stars[r]; ok {
			out[r] = sc
		}
	}
	return out, nil
}

func (f fakeFetcher) BackfillHistory(_ context.Context, fullName string) ([]fetch.StarHistoryPoint, error) {
	if err, ok := f.histErr[fullName]; ok {
		return nil, err
	}
	return f.history[fullName], nil
}

func snap(fullName string, stars int) digest.RepoSnapshot {
	return digest.RepoSnapshot{
		FullName:      fullName,
		URL:           "https://github.com/" + fullName,
		Language:      "go",
		Stars:         stars,
		StarsInPeriod: 7, // page-reported value; must survive when no baseline exists
		Source:        digest.SourceTrendingHTML,
	}
}

// TestApplyFirstRunNoBaseline: first run records a snapshot but writes no delta,
// leaving the page starsInPeriod untouched (never a fabricated 0).
func TestApplyFirstRunNoBaseline(t *testing.T) {
	s := newTestStore(t)
	repos := []digest.RepoSnapshot{snap("golang/go", 1000)}

	out, res, err := Apply(context.Background(), s, repos, ApplyOptions{Date: "2026-11-01", Source: "snapshot"}, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Recorded != 1 || res.WithDelta != 0 || res.NoBaseline != 1 {
		t.Fatalf("result = %+v, want recorded=1 withDelta=0 noBaseline=1", res)
	}
	if out[0].StarsInPeriod != 7 {
		t.Fatalf("starsInPeriod = %d, want 7 (page value preserved)", out[0].StarsInPeriod)
	}
	// Snapshot must be recorded for tomorrow's baseline.
	if _, ok, _ := s.Lookup("golang/go", "2026-11-01"); !ok {
		t.Fatal("snapshot not recorded")
	}
}

// TestApplySecondRunWritesDelta: with a prior day on record, d1 is written.
func TestApplySecondRunWritesDelta(t *testing.T) {
	s := newTestStore(t)
	if _, _, err := s.Put(Snapshot{RepoID: "golang/go", Date: "2026-11-01", Stars: 1000}); err != nil {
		t.Fatal(err)
	}
	repos := []digest.RepoSnapshot{snap("golang/go", 1050)}

	out, res, err := Apply(context.Background(), s, repos, ApplyOptions{Date: "2026-11-02"}, fakeFetcher{
		stars: map[string]fetch.StarCount{"golang/go": {FullName: "golang/go", Stars: 1050}},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.WithDelta != 1 {
		t.Fatalf("withDelta = %d, want 1", res.WithDelta)
	}
	if out[0].StarsInPeriod != 50 {
		t.Fatalf("starsInPeriod = %d, want 50 (observed d1)", out[0].StarsInPeriod)
	}
}

// TestApplyNegativeDeltaPreserved: a star loss is written as a negative delta.
func TestApplyNegativeDeltaPreserved(t *testing.T) {
	s := newTestStore(t)
	_, _, _ = s.Put(Snapshot{RepoID: "a/b", Date: "2026-11-01", Stars: 200})
	repos := []digest.RepoSnapshot{snap("a/b", 180)}
	out, _, err := Apply(context.Background(), s, repos, ApplyOptions{Date: "2026-11-02"}, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out[0].StarsInPeriod != -20 {
		t.Fatalf("starsInPeriod = %d, want -20", out[0].StarsInPeriod)
	}
}

// TestApplyNilStoreIsNoop: nil store / empty date returns input unchanged.
func TestApplyNoop(t *testing.T) {
	repos := []digest.RepoSnapshot{snap("x/y", 5)}
	out, res, err := Apply(context.Background(), nil, repos, ApplyOptions{Date: "2026-11-02"}, nil)
	if err != nil || res.Recorded != 0 || out[0].StarsInPeriod != 7 {
		t.Fatalf("nil store should be no-op, got res=%+v err=%v", res, err)
	}
	out2, _, err := Apply(context.Background(), newTestStore(t), repos, ApplyOptions{Date: ""}, nil)
	if err != nil || out2[0].StarsInPeriod != 7 {
		t.Fatalf("empty date should be no-op, got %+v err=%v", out2[0], err)
	}
}

// TestApplyFetcherErrorFallsBack: FetchStars failure keeps page values.
func TestApplyFetcherErrorFallsBack(t *testing.T) {
	s := newTestStore(t)
	repos := []digest.RepoSnapshot{snap("a/b", 500)}
	out, res, err := Apply(context.Background(), s, repos, ApplyOptions{Date: "2026-11-02"}, fakeFetcher{
		starsErr: errors.New("graphql down"),
	})
	if err != nil {
		t.Fatalf("Apply should not fail on fetcher error: %v", err)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("expected a warning about FetchStars fallback")
	}
	// Recorded snapshot used page value (500); no delta yet.
	if out[0].StarsInPeriod != 7 {
		t.Fatalf("starsInPeriod = %d, want unchanged 7", out[0].StarsInPeriod)
	}
	if got, _, _ := s.Lookup("a/b", "2026-11-02"); got.Stars != 500 {
		t.Fatalf("recorded stars = %d, want page value 500", got.Stars)
	}
}

// TestApplyBackfillSeedsHistory: a first-seen repo is seeded, and d7 can use it.
func TestApplyBackfillSeedsHistory(t *testing.T) {
	s := newTestStore(t)
	repos := []digest.RepoSnapshot{snap("golang/go", 1000)}
	ff := fakeFetcher{
		stars: map[string]fetch.StarCount{"golang/go": {FullName: "golang/go", Stars: 1000}},
		history: map[string][]fetch.StarHistoryPoint{
			"golang/go": {
				{WeekStart: "2026-10-26", Stars: 900, Days: 7},
				{WeekStart: "2026-10-19", Stars: 800, Days: 7},
			},
		},
	}
	out, res, err := Apply(context.Background(), s, repos, ApplyOptions{Date: "2026-11-02", Period: "weekly", BackfillDays: 30}, ff)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Backfilled != 1 {
		t.Fatalf("backfilled = %d, want 1", res.Backfilled)
	}
	// d7 baseline at 2026-10-26 (900) -> 1000-900=100.
	if out[0].StarsInPeriod != 100 {
		t.Fatalf("starsInPeriod = %d, want 100 (d7 from backfill)", out[0].StarsInPeriod)
	}
}

// TestApplyBackfillErrorNonFatal: a backfill failure records a warning but records
// the live snapshot and continues.
func TestApplyBackfillErrorNonFatal(t *testing.T) {
	s := newTestStore(t)
	repos := []digest.RepoSnapshot{snap("a/b", 10)}
	ff := fakeFetcher{
		stars:   map[string]fetch.StarCount{"a/b": {FullName: "a/b", Stars: 10}},
		histErr: map[string]error{"a/b": errors.New("history 404")},
	}
	out, res, err := Apply(context.Background(), s, repos, ApplyOptions{Date: "2026-11-02", BackfillDays: 30}, ff)
	if err != nil {
		t.Fatalf("Apply should not fail on backfill error: %v", err)
	}
	if res.BackfillError != 1 {
		t.Fatalf("backfillError = %d, want 1", res.BackfillError)
	}
	if out[0].StarsInPeriod != 7 {
		t.Fatalf("starsInPeriod = %d, want unchanged 7 (no baseline)", out[0].StarsInPeriod)
	}
}

// TestApplyMaxCandidatesCaps verifies the candidate cap is applied and reported.
func TestApplyMaxCandidatesCaps(t *testing.T) {
	s := newTestStore(t)
	repos := []digest.RepoSnapshot{snap("a/1", 1), snap("a/2", 2), snap("a/3", 3)}
	_, res, err := Apply(context.Background(), s, repos, ApplyOptions{Date: "2026-11-02", MaxCandidates: 2}, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Recorded != 2 {
		t.Fatalf("recorded = %d, want 2 (capped)", res.Recorded)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("expected cap warning")
	}
}
