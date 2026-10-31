package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/morehao/trendradar/internal/config"
	"github.com/morehao/trendradar/internal/fetch"
	"github.com/morehao/trendradar/internal/model"
	"github.com/morehao/trendradar/internal/releasewatch"
)

type fakeReleaseFetcher struct {
	releases map[string][]fetch.Release
	tags     map[string][]fetch.Tag
}

func (f *fakeReleaseFetcher) ListReleasesPage(_ context.Context, owner, repo string, page, pageSize int) (fetch.ReleasePage, error) {
	all := f.releases[owner+"/"+repo]
	start := (page - 1) * pageSize
	if start >= len(all) {
		return fetch.ReleasePage{}, nil
	}
	end := start + pageSize
	if end > len(all) {
		end = len(all)
	}
	return fetch.ReleasePage{Releases: all[start:end], HasNext: end < len(all)}, nil
}

func (f *fakeReleaseFetcher) GetCommit(_ context.Context, owner, repo, sha string) (fetch.Commit, error) {
	var c fetch.Commit
	c.Commit.Message = "msg " + sha
	return c, nil
}

func (f *fakeReleaseFetcher) ListTagsPage(_ context.Context, owner, repo string, page, pageSize int) (fetch.TagPage, error) {
	all := f.tags[owner+"/"+repo]
	start := (page - 1) * pageSize
	if start >= len(all) {
		return fetch.TagPage{}, nil
	}
	end := start + pageSize
	if end > len(all) {
		end = len(all)
	}
	return fetch.TagPage{Tags: all[start:end], HasNext: end < len(all)}, nil
}

func boolPtr(b bool) *bool { return &b }

func TestE2EReleasesStage(t *testing.T) {
	_, work, cfg := setupRepo(t)
	cfg.Releases = config.ReleasesConfig{
		Enabled:            true,
		Period:             "daily",
		OutputDir:          "releases",
		PerRepoLimit:       5,
		MaxNotesChars:      4000,
		MaxPages:           10,
		BaselineOnFirstRun: boolPtr(false),
		FallbackToTags:     boolPtr(true),
	}
	in := fixtureInputs()
	tm := time.Unix(1700000000, 0).UTC()
	fetcher := &fakeReleaseFetcher{releases: map[string][]fetch.Release{
		"o/r": {
			{ID: 2, TagName: "v2", Name: "v2", Body: "fix bug", PublishedAt: &tm, HTMLURL: "https://github.com/o/r/releases/tag/v2"},
			{ID: 1, TagName: "v1", Name: "v1", Body: "first", PublishedAt: &tm, HTMLURL: "https://github.com/o/r/releases/tag/v1"},
		},
	}}
	opts := Options{
		Provider: model.NewFake(),
		Releases: &ReleasesOptions{
			Repos:     []string{"o/r"},
			Fetcher:   fetcher,
			StatePath: releasewatch.StatePath(work),
			CachePath: filepath.Join(work, ".git", "trendradar-release-cache.json"),
		},
	}
	r1, err := Run(context.Background(), cfg, in, opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := filepath.Join(work, "digests", "releases", "daily", "2026-10", "2026-10-30.json")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("release digest missing: %v", err)
	}
	if r1.Report.Releases == nil || r1.Report.Releases.NewReleases != 2 {
		t.Fatalf("release report = %+v", r1.Report.Releases)
	}
	if _, err := os.Stat(releasewatch.StatePath(work)); err != nil {
		t.Fatalf("release state not saved: %v", err)
	}
	// second run: no new releases, trending unchanged -> skipped, no extra commit
	r2, err := Run(context.Background(), cfg, in, opts)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !r2.Skipped {
		t.Fatalf("second run skipped=%v, want true", r2.Skipped)
	}
	if r2.Report.Releases != nil && r2.Report.Releases.NewReleases != 0 {
		t.Fatalf("second release report = %+v", r2.Report.Releases)
	}
}
