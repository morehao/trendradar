package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
		Period:             "weekly",
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
	// Weekly digests keep the ISO-week file name so the existing
	// /api/v2/releases/{date} route (which derives the name from the date) keeps
	// serving stored documents, and they live under "_weekly" so an owner named
	// "weekly" cannot collide with the aggregated directory.
	want := filepath.Join(work, "digests", "releases", "_weekly", "2026", "2026-W44.json")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("release digest missing: %v", err)
	}
	// The per-repo document is the panel's read path: one file, week prepended.
	docPath := filepath.Join(work, "digests", "releases", "o", "r-01.md")
	doc, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("repo document missing: %v", err)
	}
	if !strings.Contains(string(doc), "## 2026-10-26 ~ 2026-11-01") {
		t.Fatalf("repo document missing the week section:\n%s", doc)
	}
	if !strings.HasPrefix(string(doc), "# o/r\n") {
		t.Fatalf("repo document missing its title:\n%s", doc)
	}
	if r1.Report.Releases == nil || r1.Report.Releases.NewReleases != 2 {
		t.Fatalf("release report = %+v", r1.Report.Releases)
	}
	if r1.Report.Releases.Checked != 1 || r1.Report.Releases.Changed != 1 {
		t.Fatalf("release report = %+v, want checked=1 changed=1", r1.Report.Releases)
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
