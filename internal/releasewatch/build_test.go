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
	return model.Result{Text: `{"repo":"o/r","summaryZh":"本周发布多个版本，修复若干问题，建议升级。"}`}, nil
}

// failProv always fails, so callers exercise the degradation path.
type failProv struct{}

func (failProv) Name() string { return "fail-prov" }

func (failProv) Complete(context.Context, string, string) (model.Result, error) {
	return model.Result{}, fmt.Errorf("model down")
}

func rel(id int64, tag string) fetch.Release {
	t := time.Unix(id, 0).UTC()
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
	if g.SummaryZh == nil || *g.SummaryZh == "" {
		t.Fatalf("repo summary missing: %+v", g)
	}
	if g.PeriodStart == nil || *g.PeriodStart != "2026-10-26" || g.PeriodEnd == nil || *g.PeriodEnd != "2026-11-01" {
		t.Fatalf("period=%v..%v, want 2026-10-26..2026-11-01", g.PeriodStart, g.PeriodEnd)
	}
	// One model call for the whole repo-week (design D2).
	if rep.Calls != 1 || rep.Summarized != 1 || rep.Changed != 1 || rep.Checked != 1 {
		t.Fatalf("report=%+v, want 1 call / 1 summarized / 1 changed", rep)
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
	// Same ISO week, different day: the gate must skip before any GitHub call.
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
	if got.LastID != 2 || len(got.SeenTags) != 2 {
		t.Fatalf("baseline state=%+v", got)
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
	if rep.Degraded != 1 || rep.Summarized != 0 {
		t.Fatalf("report=%+v, want 1 degraded", rep)
	}
	if d.Repos[0].SummaryZh == nil || *d.Repos[0].SummaryZh == "" {
		t.Fatalf("degraded summary must still be present: %+v", d.Repos[0])
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
	zh := *d.Repos[0].SummaryZh
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
	b, _ := json.Marshal(map[string]any{"repo": "o/r", "summaryZh": "这是一段非常非常长的总结文本，用来验证截断逻辑不会把整段摘要丢掉而是加上省略号。"})
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
