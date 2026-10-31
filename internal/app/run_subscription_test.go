package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morehao/trendradar/internal/config"
	"github.com/morehao/trendradar/internal/fetch"
	"github.com/morehao/trendradar/internal/model"
	"github.com/morehao/trendradar/internal/releasewatch"
	"github.com/morehao/trendradar/internal/subscription"
)

// releaseOptsForTest builds a ReleasesOptions mirroring what cmd's
// buildReleasesOptions does: resolve the versioned path, read the target-branch
// file + pending, merge for the poll list, and carry the write/replay metadata.
func releaseOptsForTest(t *testing.T, work string, cfg *config.Config, date string, fetcher *fakeReleaseFetcher) *ReleasesOptions {
	t.Helper()
	subsPath, err := subscription.RepoSubscriptionsPath(work, cfg.Output.Dir, cfg.Releases.SubscriptionsFile)
	if err != nil {
		t.Fatal(err)
	}
	branch := subscription.RunBranchName(cfg.Output.BranchPrefix, cfg.Releases.Period, date)
	baseStore, err := subscription.ReadRepoOnTargetBranch(work, cfg.Output.Dir, cfg.Releases.SubscriptionsFile, branch)
	if err != nil {
		t.Fatal(err)
	}
	pend, hasPending, err := subscription.ReadPending(work)
	if err != nil {
		t.Fatal(err)
	}
	repos, _ := subscription.MergeUnique(baseStore.List(), pend)
	return &ReleasesOptions{
		Repos:             repos,
		Fetcher:           fetcher,
		StatePath:         releasewatch.StatePath(work),
		CachePath:         filepath.Join(work, ".git", "trendradar-release-cache.json"),
		SubscriptionsPath: subsPath,
		PendingRepos:      pend,
		HasPending:        hasPending,
	}
}

// oneReleaseFetcher returns a fetcher with a single release for o/r so the
// releases stage produces artifacts and the run is not skipped for lack of
// content on the first cycle.
func oneReleaseFetcher() *fakeReleaseFetcher {
	tm := time.Unix(1700000000, 0).UTC()
	return &fakeReleaseFetcher{
		releases: map[string][]fetch.Release{
			"o/r": {{ID: 1, TagName: "v1", Name: "v1", Body: "first", PublishedAt: &tm, HTMLURL: "https://github.com/o/r/releases/tag/v1"}},
		},
		tags: map[string][]fetch.Tag{},
	}
}

func releasesCfg(cfg *config.Config) {
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
}

// subscribe add 落 pending 后工作区仍干净（G6），且连续两个 run 周期都不会被
// REPO_DIRTY 拒绝 —— 证明 pending 模型绕开了 IsClean 对 tracked 文件修改的判脏。
func TestTwoCyclesNotRepoDirty(t *testing.T) {
	remote, work, cfg := setupRepo(t)
	releasesCfg(cfg)
	in := fixtureInputs()

	// Cycle 1: first run with a pending subscription add.
	if err := subscription.WritePending(work, []string{"o/r"}); err != nil {
		t.Fatal(err)
	}
	r1, err := Run(context.Background(), cfg, in, Options{Provider: model.NewFake(), Releases: releaseOptsForTest(t, work, cfg, in.Date, oneReleaseFetcher())})
	if err != nil {
		t.Fatalf("cycle 1 run: %v", err)
	}
	if r1.Skipped {
		t.Fatal("cycle 1 should not be skipped")
	}

	// Between cycles: another add writes only .git/pending; worktree stays clean.
	if err := subscription.WritePending(work, []string{"o/r", "o/two"}); err != nil {
		t.Fatal(err)
	}
	if got := gitStatusClean(t, work); !got {
		t.Fatal("worktree dirty after subscribe add between cycles")
	}

	// Cycle 2: same date, artifacts unchanged, but subscription set changed. It
	// must NOT be rejected with REPO_DIRTY and must produce a commit carrying the
	// subscription change.
	base := countRef(t, remote, "trending/daily/"+in.Date)
	r2, err := Run(context.Background(), cfg, in, Options{Provider: model.NewFake(), Releases: releaseOptsForTest(t, work, cfg, in.Date, oneReleaseFetcher())})
	if err != nil {
		t.Fatalf("cycle 2 run: %v", err)
	}
	if r2.Skipped {
		t.Fatal("cycle 2 with a subscription-only change must not be skipped")
	}
	if got := countRef(t, remote, "trending/daily/"+in.Date); got != base+1 {
		t.Fatalf("cycle 2 commits = %d, want %d", got, base+1)
	}
	// pending cleared after successful push
	if _, ok, _ := subscription.ReadPending(work); ok {
		t.Fatal("pending not cleared after successful push")
	}
}

// 只有订阅变化也产生一次 commit（产物字节不变）。修掉 filesMatch 只看产物导致
// 订阅变更被静默丢弃的 blocker #2。
func TestSubscriptionOnlyChangeCommits(t *testing.T) {
	remote, work, cfg := setupRepo(t)
	releasesCfg(cfg)
	in := fixtureInputs()

	// First run with o/r pending: artifacts + subscription committed.
	if err := subscription.WritePending(work, []string{"o/r"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), cfg, in, Options{Provider: model.NewFake(), Releases: releaseOptsForTest(t, work, cfg, in.Date, oneReleaseFetcher())}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	branch := "trending/daily/" + in.Date
	base := countRef(t, remote, branch)

	// Add another repo: artifacts will be byte-identical (same inputs, same
	// date), so without the subsDirty path this would be skipped.
	if err := subscription.WritePending(work, []string{"o/r", "o/two"}); err != nil {
		t.Fatal(err)
	}
	r, err := Run(context.Background(), cfg, in, Options{Provider: model.NewFake(), Releases: releaseOptsForTest(t, work, cfg, in.Date, oneReleaseFetcher())})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if r.Skipped {
		t.Fatal("subscription-only change must not be skipped")
	}
	if got := countRef(t, remote, branch); got != base+1 {
		t.Fatalf("commits = %d, want %d (subscription-only commit)", got, base+1)
	}
	// The committed subscription file must contain both repos.
	got, err := subscription.ReadRepoOnTargetBranch(work, cfg.Output.Dir, cfg.Releases.SubscriptionsFile, branch)
	if err != nil {
		t.Fatal(err)
	}
	list := got.List()
	if len(list) != 2 || list[0] != "o/r" || list[1] != "o/two" {
		t.Fatalf("committed subscriptions = %v, want [o/r o/two]", list)
	}
}

// 幂等：同一 add 连跑两次（pending 内容相同）+ 两次 run，第二次无新 commit。
func TestSubscribeIdempotentNoExtraCommit(t *testing.T) {
	remote, work, cfg := setupRepo(t)
	releasesCfg(cfg)
	in := fixtureInputs()
	branch := "trending/daily/" + in.Date

	// Run 1 lands o/r.
	if err := subscription.WritePending(work, []string{"o/r"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), cfg, in, Options{Provider: model.NewFake(), Releases: releaseOptsForTest(t, work, cfg, in.Date, oneReleaseFetcher())}); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	base := countRef(t, remote, branch)

	// Re-add the SAME repo (idempotent): pending equals branch content.
	if err := subscription.WritePending(work, []string{"o/r"}); err != nil {
		t.Fatal(err)
	}
	r, err := Run(context.Background(), cfg, in, Options{Provider: model.NewFake(), Releases: releaseOptsForTest(t, work, cfg, in.Date, oneReleaseFetcher())})
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if !r.Skipped {
		t.Fatal("duplicate add must not create a new commit")
	}
	if got := countRef(t, remote, branch); got != base {
		t.Fatalf("commits = %d, want %d (no new commit)", got, base)
	}
	// pending cleared because the intent already matches the branch.
	if _, ok, _ := subscription.ReadPending(work); ok {
		t.Fatal("pending should be cleared when it already matches the branch")
	}
}

// gitStatusClean reports whether tracked worktree has no changes.
func gitStatusClean(t *testing.T, dir string) bool {
	t.Helper()
	out := gitStatusOutput(t, dir)
	return out == ""
}

// gitStatusOutput runs `git status --porcelain --untracked-files=no`.
func gitStatusOutput(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=no")
	cmd.Dir = dir
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, b)
	}
	return strings.TrimSpace(string(b))
}

// Dry run must not write the subscription file, commit, or clear pending.
func TestDryRunDoesNotTouchSubscriptions(t *testing.T) {
	_, work, cfg := setupRepo(t)
	releasesCfg(cfg)
	in := fixtureInputs()
	if err := subscription.WritePending(work, []string{"o/r"}); err != nil {
		t.Fatal(err)
	}
	subsPath, err := subscription.RepoSubscriptionsPath(work, cfg.Output.Dir, cfg.Releases.SubscriptionsFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), cfg, in, Options{DryRun: true, Provider: model.NewFake(), Releases: releaseOptsForTest(t, work, cfg, in.Date, oneReleaseFetcher())}); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if _, err := os.Stat(subsPath); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote the subscription file: %v", err)
	}
	if _, ok, _ := subscription.ReadPending(work); !ok {
		t.Fatal("dry run cleared pending")
	}
}
