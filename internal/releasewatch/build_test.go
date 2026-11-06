package releasewatch

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/morehao/trendradar/internal/fetch"
	"github.com/morehao/trendradar/internal/model"
)

type fakeFetcher struct {
	releases map[string][]fetch.Release
	tags     map[string][]fetch.Tag
	calls    int
}

func (f *fakeFetcher) ListReleasesPage(_ context.Context, owner, repo string, page, pageSize int) (fetch.ReleasePage, error) {
	f.calls++
	all := f.releases[owner+"/"+repo]
	start := (page - 1) * pageSize
	if start >= len(all) {
		return fetch.ReleasePage{HasNext: false}, nil
	}
	end := start + pageSize
	if end > len(all) {
		end = len(all)
	}
	return fetch.ReleasePage{Releases: all[start:end], HasNext: end < len(all)}, nil
}

func (f *fakeFetcher) GetCommit(_ context.Context, owner, repo, sha string) (fetch.Commit, error) {
	var c fetch.Commit
	c.SHA = sha
	c.Commit.Message = "commit for " + sha
	return c, nil
}

func (f *fakeFetcher) ListTagsPage(_ context.Context, owner, repo string, page, pageSize int) (fetch.TagPage, error) {
	all := f.tags[owner+"/"+repo]
	start := (page - 1) * pageSize
	if start >= len(all) {
		return fetch.TagPage{HasNext: false}, nil
	}
	end := start + pageSize
	if end > len(all) {
		end = len(all)
	}
	return fetch.TagPage{Tags: all[start:end], HasNext: end < len(all)}, nil
}

// fakeProv answers the weekly prompt, which asks for one summary per repo.
type fakeProv struct{ calls int }

func (f *fakeProv) Name() string { return "fake-prov" }

func (f *fakeProv) Complete(_ context.Context, _, _ string) (model.Result, error) {
	return model.Result{Text: `{"repo":"o/r","summary":"本周发布多个版本，修复若干问题，建议升级。"}`}, nil
}

// failProv always fails, so callers exercise the degradation path.
type failProv struct{}

func (failProv) Name() string { return "fail-prov" }

func (failProv) Complete(context.Context, string, string) (model.Result, error) {
	return model.Result{}, fmt.Errorf("model down")
}

func rel(id int64, tag string) fetch.Release {
	// Spread publish dates around the W44 test week so period attribution and
	// ordering tests behave like real releases (id grows => later date), instead
	// of 1970 timestamps that would (correctly) land in a 1970 week.
	t := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(id-1))
	return fetch.Release{ID: id, TagName: tag, Name: tag, Body: "notes " + tag, PublishedAt: &t, HTMLURL: "https://github.com/o/r/releases/tag/" + tag}
}

func baseOpts(t *testing.T, f *fakeFetcher) Options {
	dir := t.TempDir()
	return Options{
		Repos:              []string{"o/r"},
		Date:               "2026-10-28", // Wednesday of 2026-W44
		Period:             "weekly",
		Timezone:           "Asia/Shanghai",
		WeekStart:          "monday",
		FallbackToTags:     true,
		BaselineOnFirstRun: false,
		MaxNotesChars:      4000,
		SummaryMaxChars:    1200,
		MaxPages:           10,
		Provider:           &fakeProv{},
		ProviderName:       "fake",
		ModelName:          "fake",
		StatePath:          filepath.Join(dir, "state.json"),
		CachePath:          filepath.Join(dir, "cache.json"),
	}
}

func TestBuildIncrement(t *testing.T) {
	f := &fakeFetcher{releases: map[string][]fetch.Release{"o/r": {rel(3, "v3"), rel(2, "v2"), rel(1, "v1")}}}
	o := baseOpts(t, f)
	st := NewState()
	st.Set("o/r", RepoState{LastID: 1})
	if err := st.Save(o.StatePath); err != nil {
		t.Fatal(err)
	}
	d, state, rep, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	if d.Count != 2 || len(d.Repos) != 1 {
		t.Fatalf("count=%d groups=%d, want 2/1", d.Count, len(d.Repos))
	}
	if d.Date != "2026-11-01" {
		t.Fatalf("digest date=%s, want the week end 2026-11-01", d.Date)
	}
	g := d.Repos[0]
	// New model: the weekly roll-up is empty; each version carries its own summary.
	if g.Summary != nil && *g.Summary != "" {
		t.Fatalf("weekly roll-up should be empty on new data, got %q", *g.Summary)
	}
	for _, it := range g.Releases {
		if it.Summary == nil || *it.Summary == "" {
			t.Fatalf("per-version summary missing for %s: %+v", it.Tag, it)
		}
	}
	if g.PeriodStart == nil || *g.PeriodStart != "2026-10-26" || g.PeriodEnd == nil || *g.PeriodEnd != "2026-11-01" {
		t.Fatalf("period=%v..%v, want 2026-10-26..2026-11-01", g.PeriodStart, g.PeriodEnd)
	}
	// One model call per new version (incremental unit): v3 and v2 => 2 calls.
	if rep.Calls != 2 || rep.Summarized != 2 || rep.Changed != 1 || rep.Checked != 1 {
		t.Fatalf("report=%+v, want 2 calls / 2 summarized / 1 changed", rep)
	}
	got, _ := state.Get("o/r")
	if got.LastID != 3 {
		t.Fatalf("cursor=%d, want 3", got.LastID)
	}
	if got.WeekKey != "2026-W44" || got.Status != StatusOK || got.LatestSummaryAt != "2026-11-01" {
		t.Fatalf("state=%+v, want week gate + ok + summary date", got)
	}
}

func TestBuildWeeklyGateSkipsSecondRun(t *testing.T) {
	f := &fakeFetcher{releases: map[string][]fetch.Release{"o/r": {rel(2, "v2"), rel(1, "v1")}}}
	o := baseOpts(t, f)
	o.Trigger = "schedule" // the weekly gate applies to scheduled runs only
	st := NewState()
	st.Set("o/r", RepoState{LastID: 0})
	if err := st.Save(o.StatePath); err != nil {
		t.Fatal(err)
	}
	first, firstState, _, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	if first.Count != 2 {
		t.Fatalf("first run count=%d, want 2", first.Count)
	}
	// app.Run persists the cursor only after publishing, so the gate test must
	// save it the same way before the second run.
	if err := firstState.Save(o.StatePath); err != nil {
		t.Fatal(err)
	}
	callsAfterFirst := f.calls
	// Same ISO week, different day: the scheduled gate must skip before any
	// GitHub call.
	o.Date = "2026-10-30"
	d, _, rep, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	if d.Count != 0 {
		t.Fatalf("second run count=%d, want 0 (gate)", d.Count)
	}
	if rep.Checked != 0 || rep.Skipped != 1 {
		t.Fatalf("report=%+v, want checked=0 skipped=1", rep)
	}
	if f.calls != callsAfterFirst {
		t.Fatalf("second run hit GitHub (%d -> %d calls)", callsAfterFirst, f.calls)
	}
}

// A manual run must NOT be short-circuited by the weekly gate: it re-runs the
// incremental diff. With everything already summarized there is nothing new, so
// it reports checked>0 / changed=0 rather than being silently skipped.
func TestBuildManualIgnoresWeeklyGate(t *testing.T) {
	f := &fakeFetcher{releases: map[string][]fetch.Release{"o/r": {rel(2, "v2"), rel(1, "v1")}}}
	o := baseOpts(t, f)
	o.Trigger = "manual"
	st := NewState()
	st.Set("o/r", RepoState{LastID: 0})
	if err := st.Save(o.StatePath); err != nil {
		t.Fatal(err)
	}
	_, firstState, _, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	if err := firstState.Save(o.StatePath); err != nil {
		t.Fatal(err)
	}
	// Same week, manual re-run: must still check the repo (not already_done).
	o.Date = "2026-10-30"
	d, _, rep, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Checked != 1 {
		t.Fatalf("manual re-run report=%+v, want checked=1 (must not be gated)", rep)
	}
	if d.Count != 0 {
		t.Fatalf("manual re-run count=%d, want 0 (nothing new to summarize)", d.Count)
	}
	if rep.Changed != 0 {
		t.Fatalf("manual re-run changed=%d, want 0 (already summarized)", rep.Changed)
	}
}

func TestBuildNextWeekResumes(t *testing.T) {
	f := &fakeFetcher{releases: map[string][]fetch.Release{"o/r": {rel(3, "v3"), rel(2, "v2"), rel(1, "v1")}}}
	o := baseOpts(t, f)
	st := NewState()
	st.Set("o/r", RepoState{LastID: 1, WeekKey: "2026-W43"})
	if err := st.Save(o.StatePath); err != nil {
		t.Fatal(err)
	}
	d, _, rep, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	if d.Count != 2 || rep.Checked != 1 {
		t.Fatalf("count=%d report=%+v, want the new week to run", d.Count, rep)
	}
}

func TestBuildBaseline(t *testing.T) {
	f := &fakeFetcher{releases: map[string][]fetch.Release{"o/r": {rel(2, "v2"), rel(1, "v1")}}}
	o := baseOpts(t, f)
	o.BaselineOnFirstRun = true
	d, state, rep, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	if d.Count != 0 {
		t.Fatalf("baseline count=%d, want 0", d.Count)
	}
	if rep.Checked != 1 {
		t.Fatalf("baseline report=%+v, want checked=1", rep)
	}
	got, _ := state.Get("o/r")
	// Baseline deliberately leaves LastID at 0 and records no summarized tag: the
	// first regular run then diffs by SummarizedTags and summarizes the baseline
	// page instead of silently swallowing the versions in between (the rc.1 bug).
	if got.LastID != 0 || len(got.SeenTags) != 2 || len(got.SummarizedTags) != 0 {
		t.Fatalf("baseline state=%+v, want LastID=0, 2 seen, 0 summarized", got)
	}
	if got.Status != StatusBaseline || got.WeekKey != "2026-W44" {
		t.Fatalf("baseline status=%q week=%q, want baseline + gate week", got.Status, got.WeekKey)
	}
}

func TestBuildTagFallback(t *testing.T) {
	f := &fakeFetcher{tags: map[string][]fetch.Tag{"o/r": {{Name: "v2"}, {Name: "v1"}}}}
	o := baseOpts(t, f)
	d, _, rep, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	if d.Count != 2 || d.Repos[0].Releases[0].Kind != "tag" {
		t.Fatalf("digest=%+v", d)
	}
	if rep.Changed != 1 {
		t.Fatalf("report=%+v, want 1 changed repo", rep)
	}
}

// The weekly summary must cover every new version even though the document only
// lists the first N: there is no longer a fetch-side cap.
func TestBuildKeepsEveryNewVersion(t *testing.T) {
	f := &fakeFetcher{releases: map[string][]fetch.Release{"o/r": {
		rel(7, "v7"), rel(6, "v6"), rel(5, "v5"), rel(4, "v4"), rel(3, "v3"), rel(2, "v2"), rel(1, "v1"),
	}}}
	o := baseOpts(t, f)
	d, state, rep, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	if d.Count != 7 {
		t.Fatalf("count=%d, want all 7 versions", d.Count)
	}
	if rep.Truncated != 0 {
		t.Fatalf("truncated=%d, want 0 (no fetch-side cap)", rep.Truncated)
	}
	got, _ := state.Get("o/r")
	if got.LastID != 7 {
		t.Fatalf("cursor=%d, want 7", got.LastID)
	}
	if d.Repos[0].Releases[0].Tag != "v7" {
		t.Fatalf("first rendered tag=%s, want v7 (newest first)", d.Repos[0].Releases[0].Tag)
	}
}

func TestBuildBaselineTagOnly(t *testing.T) {
	f := &fakeFetcher{tags: map[string][]fetch.Tag{"o/r": {{Name: "v2"}, {Name: "v1"}}}}
	o := baseOpts(t, f)
	o.BaselineOnFirstRun = true
	_, state, _, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := state.Get("o/r")
	if len(got.SeenTags) != 2 || got.LastTag != "v2" {
		t.Fatalf("tag baseline state=%+v", got)
	}
	// second run in the same week must be gated, so historical tags never
	// reappear as new.
	f2 := &fakeFetcher{tags: map[string][]fetch.Tag{"o/r": {{Name: "v2"}, {Name: "v1"}}}}
	if err := state.Save(o.StatePath); err != nil {
		t.Fatal(err)
	}
	d, _, _, err := Build(context.Background(), f2, o)
	if err != nil {
		t.Fatal(err)
	}
	if d.Count != 0 {
		t.Fatalf("second run count=%d, want 0", d.Count)
	}
}

func TestBuildFailureIsolated(t *testing.T) {
	f := &fakeFetcher{releases: map[string][]fetch.Release{
		"o/bad": nil, "o/r": {rel(1, "v1")},
	}}
	o := baseOpts(t, f)
	o.Repos = []string{"o/bad", "o/r"}
	// make bad repo fail by returning an error on page 1
	ff := &errFetcher{base: f}
	d, state, rep, err := Build(context.Background(), ff, o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Failed != 1 || len(d.Repos) != 1 || d.Repos[0].FullName != "o/r" {
		t.Fatalf("digest=%+v report=%+v", d, rep)
	}
	// A failed check must not close the week: the cursor did not advance, so a
	// retry has to be possible.
	bad, _ := state.Get("o/bad")
	if bad.WeekKey != "" || bad.Status != StatusFailed {
		t.Fatalf("failed state=%+v, want status failed and no week gate", bad)
	}
}

func TestBuildDegradesOnModelFailure(t *testing.T) {
	f := &fakeFetcher{releases: map[string][]fetch.Release{"o/r": {rel(2, "v2"), rel(1, "v1")}}}
	o := baseOpts(t, f)
	o.Provider = failProv{}
	d, _, rep, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	if d.Count != 2 || !d.Degraded {
		t.Fatalf("digest=%+v, want degraded but not empty", d)
	}
	// Each of the two versions degraded independently.
	if rep.Degraded != 2 || rep.Summarized != 0 {
		t.Fatalf("report=%+v, want 2 degraded", rep)
	}
	for _, it := range d.Repos[0].Releases {
		if it.Summary == nil || *it.Summary == "" {
			t.Fatalf("degraded per-version summary must still be present for %s: %+v", it.Tag, it)
		}
	}
}

func TestBuildTruncatesLongSummary(t *testing.T) {
	f := &fakeFetcher{releases: map[string][]fetch.Release{"o/r": {rel(1, "v1")}}}
	o := baseOpts(t, f)
	o.SummaryMaxChars = 20
	o.Provider = &longProv{}
	d, _, rep, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	zh := *d.Repos[0].Releases[0].Summary
	if n := len([]rune(zh)); n != 20 {
		t.Fatalf("summary runes=%d, want 20 (truncated with ellipsis)", n)
	}
	if rep.Truncated != 1 {
		t.Fatalf("report=%+v, want truncated=1", rep)
	}
}

type longProv struct{}

func (longProv) Name() string { return "long-prov" }

func (longProv) Complete(context.Context, string, string) (model.Result, error) {
	b, _ := json.Marshal(map[string]any{"repo": "o/r", "summary": "这是一段非常非常长的总结文本，用来验证截断逻辑不会把整段摘要丢掉而是加上省略号。"})
	return model.Result{Text: string(b)}, nil
}

func TestBuildRateLimitStopsRemainingRepos(t *testing.T) {
	f := &fakeFetcher{releases: map[string][]fetch.Release{
		"o/a": {rel(1, "v1")}, "o/b": {rel(1, "v1")}, "o/c": {rel(1, "v1")},
	}}
	o := baseOpts(t, f)
	o.Repos = []string{"o/a", "o/b", "o/c"}
	o.Concurrency = 1 // deterministic order: the first repo hits the wall
	_, _, rep, err := Build(context.Background(), &rateLimitFetcher{base: f}, o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.RateLimited < 1 {
		t.Fatalf("report=%+v, want at least one rate-limited repo", rep)
	}
	if rep.Checked+rep.Skipped != 3 {
		t.Fatalf("report=%+v, want every repo accounted for", rep)
	}
}

type rateLimitFetcher struct{ base *fakeFetcher }

func (e *rateLimitFetcher) ListReleasesPage(context.Context, string, string, int, int) (fetch.ReleasePage, error) {
	return fetch.ReleasePage{}, &fetch.Error{Code: "FETCH_RATE_LIMITED", Message: "rate limited"}
}
func (e *rateLimitFetcher) GetCommit(ctx context.Context, owner, repo, sha string) (fetch.Commit, error) {
	return e.base.GetCommit(ctx, owner, repo, sha)
}
func (e *rateLimitFetcher) ListTagsPage(context.Context, string, string, int, int) (fetch.TagPage, error) {
	return fetch.TagPage{}, nil
}

type errFetcher struct{ base *fakeFetcher }

func (e *errFetcher) ListReleasesPage(ctx context.Context, owner, repo string, page, size int) (fetch.ReleasePage, error) {
	if repo == "bad" {
		return fetch.ReleasePage{}, fmt.Errorf("boom")
	}
	return e.base.ListReleasesPage(ctx, owner, repo, page, size)
}
func (e *errFetcher) GetCommit(ctx context.Context, owner, repo, sha string) (fetch.Commit, error) {
	return e.base.GetCommit(ctx, owner, repo, sha)
}

func (e *errFetcher) ListTagsPage(ctx context.Context, owner, repo string, page, size int) (fetch.TagPage, error) {
	return e.base.ListTagsPage(ctx, owner, repo, page, size)
}

// Regression: a baseline must not swallow versions published after it. Before
// the fix, baseline set LastID to the newest page release, so a version released
// afterwards (rc.1) was permanently hidden by the `r.ID <= LastID` cursor.
func TestBuildBaselineDoesNotSwallowLaterRelease(t *testing.T) {
	// Baseline sees alpha.3 (id 100) only.
	f1 := &fakeFetcher{releases: map[string][]fetch.Release{"o/r": {rel(100, "dsh-v0.2.2-alpha.3")}}}
	o := baseOpts(t, f1)
	o.BaselineOnFirstRun = true
	_, baseState, _, err := Build(context.Background(), f1, o)
	if err != nil {
		t.Fatal(err)
	}
	if err := baseState.Save(o.StatePath); err != nil {
		t.Fatal(err)
	}
	// Later, rc.1 (id 200) is published.
	f2 := &fakeFetcher{releases: map[string][]fetch.Release{"o/r": {rel(200, "dsh-v0.2.2-rc.1"), rel(100, "dsh-v0.2.2-alpha.3")}}}
	d, st, rep, err := Build(context.Background(), f2, o)
	if err != nil {
		t.Fatal(err)
	}
	var tags []string
	for _, r := range d.Repos {
		for _, x := range r.Releases {
			tags = append(tags, x.Tag)
		}
	}
	found := false
	for _, tg := range tags {
		if tg == "dsh-v0.2.2-rc.1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("rc.1 not picked up after baseline; got tags=%v", tags)
	}
	if rep.Checked != 1 || d.Count == 0 {
		t.Fatalf("report=%+v count=%d, want the later release summarized", rep, d.Count)
	}
	got, _ := st.Get("o/r")
	if !hasSummarized(got, "dsh-v0.2.2-rc.1") {
		t.Fatalf("rc.1 not recorded in SummarizedTags: %+v", got.SummarizedTags)
	}
}

// Backward compatibility: a v2 state file has only LastID (no SummarizedTags).
// An upgrade must not re-summarize the whole history — the legacy LastID
// watermark still bounds the diff.
func TestBuildLegacyLastIDStateNoResummarize(t *testing.T) {
	f := &fakeFetcher{releases: map[string][]fetch.Release{"o/r": {rel(3, "v3"), rel(2, "v2"), rel(1, "v1")}}}
	o := baseOpts(t, f)
	st := NewState()
	// Legacy shape: watermark at v2, empty SummarizedTags.
	st.Set("o/r", RepoState{LastID: 2, LastTag: "v2"})
	if err := st.Save(o.StatePath); err != nil {
		t.Fatal(err)
	}
	d, _, rep, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	// Only v3 (id 3 > LastID 2) should be new; v1/v2 must not be re-summarized.
	if d.Count != 1 || rep.Changed != 1 {
		t.Fatalf("legacy state re-summarized history: count=%d report=%+v", d.Count, rep)
	}
	var tags []string
	for _, r := range d.Repos {
		for _, x := range r.Releases {
			tags = append(tags, x.Tag)
		}
	}
	if len(tags) != 1 || tags[0] != "v3" {
		t.Fatalf("want only v3, got %v", tags)
	}
}

// A forced run re-summarizes versions that are already in the incremental cursor
// (the "re-generate" button), instead of stopping at SummarizedTags. Non-forced
// runs keep the cursor and produce nothing new.
func TestBuildForceResummarizesSummarized(t *testing.T) {
	f := &fakeFetcher{releases: map[string][]fetch.Release{"o/r": {rel(2, "v2"), rel(1, "v1")}}}
	o := baseOpts(t, f)
	o.Trigger = "manual"
	st := NewState()
	st.Set("o/r", RepoState{LastID: 0})
	if err := st.Save(o.StatePath); err != nil {
		t.Fatal(err)
	}
	_, firstState, _, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	if err := firstState.Save(o.StatePath); err != nil {
		t.Fatal(err)
	}
	// Non-forced re-run: everything already summarized -> nothing new.
	_, _, rep, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Changed != 0 || rep.NewReleases != 0 {
		t.Fatalf("non-forced re-run report=%+v, want no change", rep)
	}
	// Forced re-run: re-summarize both versions.
	o.Force = true
	f.calls = 0
	_, _, repF, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	if repF.NewReleases != 2 || repF.Summarized != 2 {
		t.Fatalf("forced re-run report=%+v, want 2 re-summarized", repF)
	}
}
