package releasewatch

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/morehao/trendradar/internal/fetch"
	"github.com/morehao/trendradar/internal/model"
)

type fakeFetcher struct {
	releases map[string][]fetch.Release
	tags     map[string][]fetch.Tag
}

func (f *fakeFetcher) ListReleasesPage(_ context.Context, owner, repo string, page, pageSize int) (fetch.ReleasePage, error) {
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

type fakeProv struct{}

func (fakeProv) Name() string { return "fake-prov" }

var repoTagRe = regexp.MustCompile(`(?m)^REPO=(\S+)\nTAG=(\S+)$`)

func (fakeProv) Complete(_ context.Context, _, user string) (model.Result, error) {
	var items []map[string]string
	for _, m := range repoTagRe.FindAllStringSubmatch(user, -1) {
		items = append(items, map[string]string{"repo": m[1], "tag": m[2], "summaryZh": "该版本新增了核心能力并修复若干问题，建议升级。"})
	}
	b, _ := json.Marshal(map[string]any{"summaries": items})
	return model.Result{Text: string(b)}, nil
}

func rel(id int64, tag string) fetch.Release {
	t := time.Unix(id, 0).UTC()
	return fetch.Release{ID: id, TagName: tag, Name: tag, Body: "notes " + tag, PublishedAt: &t, HTMLURL: "https://github.com/o/r/releases/tag/" + tag}
}

func baseOpts(t *testing.T, f *fakeFetcher) Options {
	dir := t.TempDir()
	return Options{
		Repos:              []string{"o/r"},
		Date:               "2026-10-31",
		Period:             "daily",
		FallbackToTags:     true,
		BaselineOnFirstRun: false,
		PerRepoLimit:       5,
		MaxNotesChars:      4000,
		MaxPages:           10,
		Provider:           fakeProv{},
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
	got, _ := state.Get("o/r")
	if got.LastID != 3 {
		t.Fatalf("cursor=%d, want 3", got.LastID)
	}
	if rep.Summarized != 2 || rep.Degraded != 0 {
		t.Fatalf("report=%+v", rep)
	}
}

func TestBuildBaseline(t *testing.T) {
	f := &fakeFetcher{releases: map[string][]fetch.Release{"o/r": {rel(2, "v2"), rel(1, "v1")}}}
	o := baseOpts(t, f)
	o.BaselineOnFirstRun = true
	d, state, _, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	if d.Count != 0 {
		t.Fatalf("baseline count=%d, want 0", d.Count)
	}
	got, _ := state.Get("o/r")
	if got.LastID != 2 || len(got.SeenTags) != 2 {
		t.Fatalf("baseline state=%+v", got)
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
	if rep.Summarized != 2 {
		t.Fatalf("report=%+v", rep)
	}
}

func TestBuildPerRepoLimitOldestFirst(t *testing.T) {
	f := &fakeFetcher{releases: map[string][]fetch.Release{"o/r": {
		rel(7, "v7"), rel(6, "v6"), rel(5, "v5"), rel(4, "v4"), rel(3, "v3"), rel(2, "v2"), rel(1, "v1"),
	}}}
	o := baseOpts(t, f)
	o.PerRepoLimit = 5
	d, state, rep, err := Build(context.Background(), f, o)
	if err != nil {
		t.Fatal(err)
	}
	if d.Count != 5 {
		t.Fatalf("count=%d, want 5", d.Count)
	}
	got, _ := state.Get("o/r")
	if got.LastID != 5 {
		t.Fatalf("cursor=%d, want 5 (oldest five processed)", got.LastID)
	}
	if rep.Truncated != 1 {
		t.Fatalf("truncated=%d, want 1", rep.Truncated)
	}
	if d.Repos[0].Releases[0].Tag != "v5" {
		t.Fatalf("first rendered tag=%s, want v5", d.Repos[0].Releases[0].Tag)
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
	// second run must not re-add historical tags
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
	d, _, rep, err := Build(context.Background(), ff, o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Failed != 1 || len(d.Repos) != 1 || d.Repos[0].FullName != "o/r" {
		t.Fatalf("digest=%+v report=%+v", d, rep)
	}
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
