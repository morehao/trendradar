package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/morehao/trendradar/internal/config"
	"github.com/morehao/trendradar/internal/fetch"
	"github.com/morehao/trendradar/internal/model"
	"github.com/morehao/trendradar/internal/releasewatch"
	"github.com/morehao/trendradar/internal/subscription"
)

func sha256File(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func commitFile(t *testing.T, work, rel, content string) {
	t.Helper()
	p := filepath.Join(work, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "data: subscriptions")
	runGit(t, work, "push", "origin", "main")
}

func weeklyReleasesConfig(work string) config.ReleasesConfig {
	return config.ReleasesConfig{
		Enabled:            true,
		Period:             "weekly",
		OutputDir:          "releases",
		PerRepoLimit:       5,
		MaxNotesChars:      4000,
		MaxPages:           10,
		DocMaxLines:        600,
		SummaryMaxChars:    1200,
		Timezone:           "Asia/Shanghai",
		WeekStart:          "monday",
		BaselineOnFirstRun: boolPtr(false),
		FallbackToTags:     boolPtr(true),
	}
}

// A14/A15: a release-only run writes release artifacts and nothing else. It must
// not replay the pending subscription queue, must not touch subscriptions.yaml,
// and must not render a trending digest.
func TestReleaseOnlyRunLeavesSubscriptionsAlone(t *testing.T) {
	_, work, cfg := setupRepo(t)
	cfg.Releases = weeklyReleasesConfig(work)
	subsRel := "digests/subscriptions.yaml"
	commitFile(t, work, subsRel, "schema: 1\nsubscriptions:\n  - o/r\n")
	subsPath := filepath.Join(work, filepath.FromSlash(subsRel))

	// A queued subscription change that only the daily run may land.
	if err := subscription.WritePending(work, []string{"o/r", "o/extra"}); err != nil {
		t.Fatal(err)
	}
	pending := subscription.PendingPath(work)
	before := sha256File(t, subsPath)
	pendingBefore := sha256File(t, pending)

	svc := &fakeReleaseFetcher{releases: map[string][]fetch.Release{
		"o/r": {{ID: 1, TagName: "v1", Name: "v1", Body: "first", HTMLURL: "https://github.com/o/r/releases/tag/v1"}},
	}}
	tm := time.Unix(1700000000, 0).UTC()
	svc.releases["o/r"][0].PublishedAt = &tm

	res, err := Run(context.Background(), cfg, Inputs{Date: "2026-10-30"}, Options{
		Provider: model.NewFake(),
		Stages:   []string{StageReleases},
		Releases: &ReleasesOptions{
			Repos:             []string{"o/r"},
			Fetcher:           svc,
			StatePath:         releasewatch.StatePath(work),
			CachePath:         filepath.Join(work, ".git", "trendradar-release-cache.json"),
			SubscriptionsPath: subsPath,
			PendingRepos:      []string{"o/r", "o/extra"},
			HasPending:        true,
		},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := sha256File(t, subsPath); got != before {
		t.Fatal("subscriptions.yaml changed during a release-only run (A14)")
	}
	if got := sha256File(t, pending); got != pendingBefore {
		t.Fatal("pending queue was replayed or cleared by a release-only run")
	}
	for _, rel := range []string{"digests/daily", "digests/weekly", "digests/monthly"} {
		if _, err := os.Stat(filepath.Join(work, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("trending artifact %s written by a release-only run", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(work, "digests", "releases", "o", "r-01.md")); err != nil {
		t.Fatalf("release document missing: %v", err)
	}
	// The branch follows the weekly summary period so subscribe/serve read the
	// same branch the writer commits to.
	if res.Branch != "trending/weekly/2026-W44" {
		t.Fatalf("branch=%q, want the weekly branch", res.Branch)
	}
	if res.Report.Releases == nil || res.Report.Releases.Checked != 1 {
		t.Fatalf("release report=%+v", res.Report.Releases)
	}
	if res.Digest.Count != 0 {
		t.Fatalf("digest count=%d, want no trending digest", res.Digest.Count)
	}
}

// A4 at the run level: the same week twice produces exactly one commit.
func TestReleaseOnlyRunIsIdempotentWithinWeek(t *testing.T) {
	remote, work, cfg := setupRepo(t)
	cfg.Releases = weeklyReleasesConfig(work)
	commitFile(t, work, "digests/subscriptions.yaml", "schema: 1\nsubscriptions:\n  - o/r\n")

	tm := time.Unix(1700000000, 0).UTC()
	svc := &fakeReleaseFetcher{releases: map[string][]fetch.Release{
		"o/r": {{ID: 1, TagName: "v1", Name: "v1", Body: "first", PublishedAt: &tm, HTMLURL: "https://github.com/o/r/releases/tag/v1"}},
	}}
	opts := Options{
		Provider: model.NewFake(),
		Stages:   []string{StageReleases},
		Releases: &ReleasesOptions{
			Repos:     []string{"o/r"},
			Fetcher:   svc,
			StatePath: releasewatch.StatePath(work),
			CachePath: filepath.Join(work, ".git", "trendradar-release-cache.json"),
		},
	}
	if _, err := Run(context.Background(), cfg, Inputs{Date: "2026-10-27"}, opts); err != nil {
		t.Fatalf("first run: %v", err)
	}
	base := countRef(t, remote, "trending/weekly/2026-W44")
	second, err := Run(context.Background(), cfg, Inputs{Date: "2026-10-30"}, opts)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !second.Skipped {
		t.Fatalf("second run skipped=%v, want true (weekly gate)", second.Skipped)
	}
	if got := countRef(t, remote, "trending/weekly/2026-W44"); got != base {
		t.Fatalf("second run added commits: %d -> %d", base, got)
	}
}
