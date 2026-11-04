package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/morehao/trendradar/internal/config"
	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/enrich"
	"github.com/morehao/trendradar/internal/fetch"
	"github.com/morehao/trendradar/internal/gitx"
	"github.com/morehao/trendradar/internal/ipc"
	"github.com/morehao/trendradar/internal/model"
	"github.com/morehao/trendradar/internal/pr"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func countRef(t *testing.T, gitDir, ref string) int {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", gitDir, "rev-list", "--count", ref).Output()
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n
}

func setupRepo(t *testing.T) (remote, work string, cfg *config.Config) {
	t.Helper()
	root := t.TempDir()
	remote = filepath.Join(root, "remote.git")
	work = filepath.Join(root, "work")
	runGit(t, "", "init", "--bare", "-b", "main", remote)
	runGit(t, "", "init", "-b", "main", work)
	runGit(t, work, "config", "user.name", "tester")
	runGit(t, work, "config", "user.email", "tester@example.com")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "init")
	runGit(t, work, "remote", "add", "origin", remote)
	runGit(t, work, "push", "-u", "origin", "main")
	cfg = &config.Config{
		Repo:     config.RepoConfig{Path: work, Remote: "origin", BaseBranch: "main", AuthorName: "tester", AuthorEmail: "tester@example.com"},
		Output:   config.OutputConfig{Dir: "digests", BranchPrefix: "trending/"},
		Trending: config.TrendingConfig{Since: "daily"},
	}
	return remote, work, cfg
}

func fixtureInputs() Inputs {
	mk := func(i int, name string) digest.RepoSnapshot {
		return digest.RepoSnapshot{FullName: name, URL: "https://github.com/" + name, Description: digest.StrPtr("desc"), Language: "go", Stars: 100 + i, StarsInPeriod: 10, Forks: digest.IntPtr(5), Rank: i + 1, Source: digest.SourceTrendingHTML}
	}
	return Inputs{Repos: []digest.RepoSnapshot{mk(0, "acme/one"), mk(1, "acme/two")}, Date: "2026-10-30", Period: "daily"}
}

func TestE2ECommitPushAndIdempotent(t *testing.T) {
	remote, _, cfg := setupRepo(t)
	in := fixtureInputs()

	r1, err := Run(context.Background(), cfg, in, Options{Provider: model.NewFake()})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if r1.Skipped {
		t.Fatal("first run should not be skipped")
	}
	if r1.Branch != "trending/daily/2026-10-30" {
		t.Fatalf("branch = %q", r1.Branch)
	}
	base := countRef(t, remote, "main")
	if got := countRef(t, remote, "trending/daily/2026-10-30"); got != base+1 {
		t.Fatalf("remote branch commits = %d, want %d", got, base+1)
	}

	r2, err := Run(context.Background(), cfg, in, Options{Provider: model.NewFake()})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !r2.Skipped {
		t.Fatal("second run should be skipped (same contentHash)")
	}
	if got := countRef(t, remote, "trending/daily/2026-10-30"); got != base+1 {
		t.Fatalf("second run added commits: got %d, want %d", got, base+1)
	}
}

func TestRepoDirty(t *testing.T) {
	_, work, cfg := setupRepo(t)
	in := fixtureInputs()
	// modify a tracked file
	f, err := os.OpenFile(filepath.Join(work, "README.md"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintln(f, "dirty change")
	_ = f.Close()

	_, err = Run(context.Background(), cfg, in, Options{Provider: model.NewFake()})
	if err == nil {
		t.Fatal("expected REPO_DIRTY")
	}
	var ge *gitx.Error
	if !errors.As(err, &ge) || ge.Code != ipc.CodeRepoDirty {
		t.Fatalf("err = %v, want REPO_DIRTY", err)
	}
}

// Regression: a repo that already tracks digests/index.md on a previous date's
// branch must not abort when switching to a new date's branch.
func TestSecondDateDoesNotBlockOnTrackedIndex(t *testing.T) {
	remote, _, cfg := setupRepo(t)
	if _, err := Run(context.Background(), cfg, fixtureInputs(), Options{Provider: model.NewFake()}); err != nil {
		t.Fatalf("first date run: %v", err)
	}
	in2 := fixtureInputs()
	in2.Date = "2026-10-31"
	r, err := Run(context.Background(), cfg, in2, Options{Provider: model.NewFake()})
	if err != nil {
		t.Fatalf("second date run: %v", err)
	}
	if r.Branch != "trending/daily/2026-10-31" {
		t.Fatalf("branch = %q", r.Branch)
	}
	base := countRef(t, remote, "main")
	if got := countRef(t, remote, "trending/daily/2026-10-31"); got != base+1 {
		t.Fatalf("branch commits = %d, want %d", got, base+1)
	}
}

// Regression: a non-default output.dir must be both written AND staged. Before
// the fix the renderer hard-coded "digests" while git staged output.dir, so the
// run failed with `fatal: pathspec 'trending' did not match any files`.
func TestCustomOutputDirHonored(t *testing.T) {
	remote, work, cfg := setupRepo(t)
	cfg.Output.Dir = "trending"
	r, err := Run(context.Background(), cfg, fixtureInputs(), Options{Provider: model.NewFake()})
	if err != nil {
		t.Fatalf("run with custom output dir: %v", err)
	}
	if r.Branch != "trending/daily/2026-10-30" {
		t.Fatalf("branch = %q", r.Branch)
	}
	base := countRef(t, remote, "main")
	if got := countRef(t, remote, "trending/daily/2026-10-30"); got != base+1 {
		t.Fatalf("branch commits = %d, want %d", got, base+1)
	}
	for _, rel := range []string{"trending/daily/2026-10/2026-10-30.md", "trending/daily/2026-10/2026-10-30.json", "trending/index.md"} {
		if _, err := os.Stat(filepath.Join(work, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("missing %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(work, "digests")); !os.IsNotExist(err) {
		t.Fatalf("unexpected digests/ dir created: %v", err)
	}
}

type badProvider struct{}

func (badProvider) Name() string { return "bad" }
func (badProvider) Complete(context.Context, string, string) (model.Result, error) {
	return model.Result{Text: "not json"}, nil
}

// A degraded model must be reflected in digest.degraded and surfaced as a
// warning plus a visible notice in the markdown. Previously a wrong key silently
// produced an all-English digest that still claimed degraded=false.
func TestDegradedSummariesMarkedAndWarned(t *testing.T) {
	_, work, cfg := setupRepo(t)
	r, err := Run(context.Background(), cfg, fixtureInputs(), Options{Provider: badProvider{}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !r.Digest.Degraded {
		t.Fatal("digest.degraded should be true when summaries fall back to original")
	}
	if len(r.Report.Warnings) == 0 {
		t.Fatal("expected a degradation warning")
	}
	md, err := os.ReadFile(filepath.Join(work, "digests", "daily", "2026-10", "2026-10-30.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "降级") {
		t.Fatalf("markdown is missing the degraded notice:\n%s", md)
	}
}

type fakePR struct {
	latest     *pr.Pull // FindForBranch result (any state)
	existing   *pr.Pull // FindOpen result (open PR)
	created    *pr.Pull
	createdErr error
	merged     []int
	creates    int
	deleted    []string
	// latestAfterCreate, when set, is what FindForBranch returns once a PR has
	// been created — lets a test model "the new PR exists in some state".
	latestAfterCreate *pr.Pull
}

func (f *fakePR) FindOpen(context.Context, string, string) (*pr.Pull, error) { return f.existing, nil }
func (f *fakePR) FindForBranch(context.Context, string, string) (*pr.Pull, error) {
	if f.creates > 0 && f.latestAfterCreate != nil {
		return f.latestAfterCreate, nil
	}
	return f.latest, nil
}
func (f *fakePR) Create(context.Context, string, string, string, string) (*pr.Pull, error) {
	f.creates++
	if f.createdErr != nil {
		return nil, f.createdErr
	}
	return f.created, nil
}
func (f *fakePR) Update(context.Context, int, string, string) (*pr.Pull, error) {
	return f.existing, nil
}
func (f *fakePR) Merge(_ context.Context, n int) error {
	f.merged = append(f.merged, n)
	return nil
}
func (f *fakePR) DeleteBranch(_ context.Context, branch string) error {
	f.deleted = append(f.deleted, branch)
	return nil
}

// Regression: autoMerge must apply to a freshly created PR, not only to a PR
// that already existed on a previous run.
func TestOpenPRAutoMergesNewPR(t *testing.T) {
	fake := &fakePR{created: &pr.Pull{Number: 11, HTMLURL: "https://github.com/acme/widgets/pull/11"}}
	cfg := &config.Config{
		Repo: config.RepoConfig{Remote: "origin", BaseBranch: "main"},
		PR:   config.PRConfig{Enabled: true, AutoMerge: true},
	}
	url, err := openPR(context.Background(), fake, cfg, "trending/daily/2026-10-30", digest.Digest{Date: "2026-10-30", Count: 3})
	if err != nil {
		t.Fatalf("openPR: %v", err)
	}
	if url != fake.created.HTMLURL {
		t.Fatalf("url = %q, want %q", url, fake.created.HTMLURL)
	}
	if len(fake.merged) != 1 || fake.merged[0] != 11 {
		t.Fatalf("new PR was not auto-merged: %v", fake.merged)
	}
}

func TestOpenPRMergesExisting(t *testing.T) {
	fake := &fakePR{existing: &pr.Pull{Number: 9, HTMLURL: "https://github.com/acme/widgets/pull/9"}}
	cfg := &config.Config{
		Repo: config.RepoConfig{Remote: "origin", BaseBranch: "main"},
		PR:   config.PRConfig{Enabled: true, AutoMerge: true},
	}
	if _, err := openPR(context.Background(), fake, cfg, "trending/daily/2026-10-30", digest.Digest{Date: "2026-10-30", Count: 1}); err != nil {
		t.Fatalf("openPR: %v", err)
	}
	if len(fake.merged) != 1 || fake.merged[0] != 9 {
		t.Fatalf("existing PR merge = %v", fake.merged)
	}
}

// withPRClient installs a fake PR client for the duration of a test.
func withPRClient(t *testing.T, fake prAPI) {
	t.Helper()
	old := newPRClientFor
	newPRClientFor = func(context.Context, *gitx.Git, *config.Config, string) (prAPI, error) { return fake, nil }
	t.Cleanup(func() { newPRClientFor = old })
}

func refSHA(t *testing.T, gitDir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", gitDir, "rev-parse", ref).Output()
	if err != nil {
		t.Fatalf("rev-parse %s: %v", ref, err)
	}
	return strings.TrimSpace(string(out))
}

// Regression (C1): the original failure. A same-date rerun with changed content
// rebuilds the branch from origin/main, so the remote branch is no longer an
// ancestor of the new commit; a plain push is rejected as non-fast-forward. The
// core must force-with-lease over its own branch instead.
func TestSameDateRerunAppendsCommit(t *testing.T) {
	remote, _, cfg := setupRepo(t)
	if _, err := Run(context.Background(), cfg, fixtureInputs(), Options{Provider: model.NewFake()}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	base := countRef(t, remote, "main")
	first := refSHA(t, remote, "trending/daily/2026-10-30")

	in2 := fixtureInputs()
	in2.Repos[0].Stars += 99 // same date, different content
	r2, err := Run(context.Background(), cfg, in2, Options{Provider: model.NewFake()})
	if err != nil {
		t.Fatalf("same-date rerun with new content: %v", err)
	}
	if r2.Skipped {
		t.Fatal("rerun with changed content must not be skipped")
	}
	second := refSHA(t, remote, "trending/daily/2026-10-30")
	if second == first {
		t.Fatalf("remote branch was not updated; still %s", first)
	}
	if got, want := countRef(t, remote, "trending/daily/2026-10-30"), base+2; got != want {
		t.Fatalf("remote branch commits = %d, want %d (append, not force-replace)", got, want)
	}
}

// A push that succeeds but is followed by a PR failure must be retried on the
// next run without inventing a new commit: the content is already on the branch,
// only the PR is missing.
func TestPRFailureRetriedWithoutNewCommit(t *testing.T) {
	remote, _, cfg := setupRepo(t)
	cfg.PR = config.PRConfig{Enabled: true, AutoMerge: true}
	fake := &fakePR{createdErr: errors.New("boom")}
	withPRClient(t, fake)

	in := fixtureInputs()
	r1, err := Run(context.Background(), cfg, in, Options{Provider: model.NewFake(), Token: "tok"})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if r1.Skipped {
		t.Fatal("first run should not be skipped")
	}
	base := countRef(t, remote, "main")

	fake.createdErr = nil
	fake.created = &pr.Pull{Number: 9, HTMLURL: "https://github.com/acme/widgets/pull/9"}
	fake.creates = 0
	r2, err := Run(context.Background(), cfg, in, Options{Provider: model.NewFake(), Token: "tok"})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := countRef(t, remote, "trending/daily/2026-10-30"); got != base+1 {
		t.Fatalf("retry added a commit: got %d, want %d", got, base+1)
	}
	if fake.creates != 1 {
		t.Fatalf("PR was not retried: creates=%d", fake.creates)
	}
	if r2.PRURL != fake.created.HTMLURL {
		t.Fatalf("PRURL = %q, want %q", r2.PRURL, fake.created.HTMLURL)
	}
}

// Regression (C3): a *scheduled* run on a date whose PR was already merged must
// be skipped — automation must not reopen a finished day. A manual
// "regenerate this period" run is an explicit request and rebuilds (see
// TestMergedPRRebuildsOnManualTrigger).
func TestMergedPRSkipsScheduledRun(t *testing.T) {
	remote, _, cfg := setupRepo(t)
	cfg.PR = config.PRConfig{Enabled: true, AutoMerge: true}
	mergedAt := time.Now()
	withPRClient(t, &fakePR{latest: &pr.Pull{Number: 5, State: "closed", MergedAt: &mergedAt}})

	r, err := Run(context.Background(), cfg, fixtureInputs(), Options{Provider: model.NewFake(), Token: "tok", Trigger: TriggerSchedule})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !r.Skipped || r.SkipReason != "merged" {
		t.Fatalf("Skipped=%v reason=%q, want skipped/merged", r.Skipped, r.SkipReason)
	}
	if got := countRef(t, remote, "trending/daily/2026-10-30"); got != 0 {
		t.Fatalf("unexpected branch pushed for merged date: %d commits", got)
	}
	// Rendering happens before the PR pre-check, so even a merged/scheduled skip
	// carries the (unchanged) digest. The plugin refreshes its preview with it.
	if r.DigestPath != "digests/daily/2026-10/2026-10-30.md" {
		t.Fatalf("merged skip DigestPath = %q", r.DigestPath)
	}
}

// A manual run on a date whose PR was already merged must rebuild and open a
// fresh PR (the old one is merged, so FindOpen sees nothing and Create runs).
// Because the rebuilt PR is auto-merged, the branch is then cleaned up.
func TestMergedPRRebuildsOnManualTrigger(t *testing.T) {
	_, _, cfg := setupRepo(t)
	cfg.PR = config.PRConfig{Enabled: true, AutoMerge: true}
	mergedAt := time.Now()
	created := &pr.Pull{Number: 9, HTMLURL: "https://github.com/acme/widgets/pull/9"}
	mergedNew := &pr.Pull{Number: 9, State: "closed", MergedAt: &mergedAt}
	fake := &fakePR{
		latest:            &pr.Pull{Number: 5, State: "closed", MergedAt: &mergedAt},
		created:           created,
		latestAfterCreate: mergedNew,
	}
	withPRClient(t, fake)

	r, err := Run(context.Background(), cfg, fixtureInputs(), Options{Provider: model.NewFake(), Token: "tok", Trigger: TriggerManual})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if r.Skipped {
		t.Fatalf("a manual run on a merged date must rebuild, got skip=%q", r.SkipReason)
	}
	if fake.creates != 1 {
		t.Fatalf("expected a fresh PR to be created, creates=%d", fake.creates)
	}
	if r.PRURL != created.HTMLURL {
		t.Fatalf("PRURL = %q, want %q", r.PRURL, created.HTMLURL)
	}
	// The rebuilt PR auto-merges, so the branch is deleted via the API and the
	// local branch is removed too.
	if len(fake.deleted) != 1 || fake.deleted[0] != "trending/daily/2026-10-30" {
		t.Fatalf("branch was not deleted via API: %v", fake.deleted)
	}
}

// With autoMerge off, the rebuilt PR stays open: the branch must NOT be deleted.
func TestMergedPRRebuildKeepsBranchWhenNotMerged(t *testing.T) {
	remote, _, cfg := setupRepo(t)
	cfg.PR = config.PRConfig{Enabled: true, AutoMerge: false}
	mergedAt := time.Now()
	created := &pr.Pull{Number: 12, HTMLURL: "https://github.com/acme/widgets/pull/12"}
	// Old PR merged (manual rebuilds); new PR open (not merged) → no cleanup.
	fake := &fakePR{
		latest:            &pr.Pull{Number: 5, State: "closed", MergedAt: &mergedAt},
		created:           created,
		latestAfterCreate: &pr.Pull{Number: 12, State: "open"},
	}
	withPRClient(t, fake)

	r, err := Run(context.Background(), cfg, fixtureInputs(), Options{Provider: model.NewFake(), Token: "tok", Trigger: TriggerManual})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if r.Skipped {
		t.Fatalf("manual rebuild must not skip, got %q", r.SkipReason)
	}
	if len(fake.deleted) != 0 {
		t.Fatalf("branch must not be deleted when PR is not merged: %v", fake.deleted)
	}
	if got := countRef(t, remote, "trending/daily/2026-10-30"); got == 0 {
		t.Fatalf("branch should still exist when PR is open")
	}
}

// Regression (C3): a closed-unmerged PR must be rebuilt and re-opened, not
// skipped.
func TestClosedPRRebuildsAndReopens(t *testing.T) {
	remote, _, cfg := setupRepo(t)
	cfg.PR = config.PRConfig{Enabled: true, AutoMerge: true}
	created := &pr.Pull{Number: 7, HTMLURL: "https://github.com/acme/widgets/pull/7"}
	fake := &fakePR{latest: &pr.Pull{Number: 3, State: "closed", MergedAt: nil}, created: created}
	withPRClient(t, fake)

	r, err := Run(context.Background(), cfg, fixtureInputs(), Options{Provider: model.NewFake(), Token: "tok"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if r.Skipped {
		t.Fatal("closed PR must trigger a rebuild, not a skip")
	}
	if fake.creates != 1 {
		t.Fatalf("expected a fresh PR to be created, creates=%d", fake.creates)
	}
	base := countRef(t, remote, "main")
	if got := countRef(t, remote, "trending/daily/2026-10-30"); got != base+1 {
		t.Fatalf("branch commits = %d, want %d", got, base+1)
	}
	if r.PRURL != created.HTMLURL {
		t.Fatalf("PRURL = %q, want %q", r.PRURL, created.HTMLURL)
	}
}

// A scheduled run must respect a human closing the PR: it neither rebuilds the
// branch nor reopens the PR. A manual run is an explicit request and may.
func TestClosedPRSkippedOnScheduledRun(t *testing.T) {
	remote, _, cfg := setupRepo(t)
	cfg.PR = config.PRConfig{Enabled: true, AutoMerge: true}
	fake := &fakePR{latest: &pr.Pull{Number: 3, State: "closed", MergedAt: nil}}
	withPRClient(t, fake)

	r, err := Run(context.Background(), cfg, fixtureInputs(), Options{
		Provider: model.NewFake(), Token: "tok", Trigger: TriggerSchedule,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !r.Skipped || r.SkipReason != "pr-closed" {
		t.Fatalf("Skipped=%v reason=%q, want skipped/pr-closed", r.Skipped, r.SkipReason)
	}
	if fake.creates != 0 {
		t.Fatalf("scheduled run reopened a closed PR: creates=%d", fake.creates)
	}
	if got := countRef(t, remote, "trending/daily/2026-10-30"); got != 0 {
		t.Fatalf("scheduled run pushed a rejected date: %d commits", got)
	}
}

// A remote branch deleted after its PR was closed must be re-creatable. This is
// the exact state that produced "(stale info)" with force-with-lease.
func TestRemoteBranchDeletedIsRecreated(t *testing.T) {
	remote, _, cfg := setupRepo(t)
	if _, err := Run(context.Background(), cfg, fixtureInputs(), Options{Provider: model.NewFake()}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	base := countRef(t, remote, "main")
	runGit(t, "", "--git-dir", remote, "update-ref", "-d", "refs/heads/trending/daily/2026-10-30")

	in2 := fixtureInputs()
	in2.Repos[0].Stars += 7
	r2, err := Run(context.Background(), cfg, in2, Options{Provider: model.NewFake()})
	if err != nil {
		t.Fatalf("rerun after remote branch deletion: %v", err)
	}
	if r2.Skipped {
		t.Fatal("changed content after the remote branch was deleted must not be skipped")
	}
	if got, want := countRef(t, remote, "trending/daily/2026-10-30"), base+2; got != want {
		t.Fatalf("recreated branch commits = %d, want %d", got, want)
	}
}

// Regression (C4): changing only output.dir must re-render instead of being
// skipped on date+contentHash.
func TestOutputDirChangeIsNotSkipped(t *testing.T) {
	_, work, cfg := setupRepo(t)
	in := fixtureInputs()
	if _, err := Run(context.Background(), cfg, in, Options{Provider: model.NewFake()}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	cfg.Output.Dir = "digests-alt"
	r2, err := Run(context.Background(), cfg, in, Options{Provider: model.NewFake()})
	if err != nil {
		t.Fatalf("run with new output dir: %v", err)
	}
	if r2.Skipped {
		t.Fatal("changing output.dir must not hit the contentHash skip")
	}
	rel := "digests-alt/daily/2026-10/2026-10-30.md"
	if _, err := os.Stat(filepath.Join(work, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("missing %s after output.dir change: %v", rel, err)
	}
}

// G2: the same date must produce three distinct branches/artifacts for
// daily/weekly/monthly without overwriting each other.
func TestSameDayThreePeriodsDistinctBranches(t *testing.T) {
	remote, _, cfg := setupRepo(t)
	base := countRef(t, remote, "main")
	branches := map[string]bool{}
	for _, period := range []string{"daily", "weekly", "monthly"} {
		in := fixtureInputs()
		in.Date = "2026-11-01"
		in.Period = period
		r, err := Run(context.Background(), cfg, in, Options{Provider: model.NewFake()})
		if err != nil {
			t.Fatalf("%s run: %v", period, err)
		}
		if r.Skipped {
			t.Fatalf("%s run unexpectedly skipped", period)
		}
		branches[r.Branch] = true
	}
	want := []string{"trending/daily/2026-11-01", "trending/weekly/2026-W44", "trending/monthly/2026-11"}
	for _, b := range want {
		if !branches[b] {
			t.Fatalf("missing branch %q in %v", b, branches)
		}
		if got := countRef(t, remote, b); got != base+1 {
			t.Fatalf("%s commits = %d, want %d", b, got, base+1)
		}
	}
	if len(branches) != 3 {
		t.Fatalf("want 3 distinct branches, got %v", branches)
	}
}

// G3: two daily runs must accumulate two rows in index.md, linked under daily/.
func TestIndexAccumulatesTwoDates(t *testing.T) {
	_, work, cfg := setupRepo(t)
	if _, err := Run(context.Background(), cfg, fixtureInputs(), Options{Provider: model.NewFake()}); err != nil {
		t.Fatalf("first date: %v", err)
	}
	in2 := fixtureInputs()
	in2.Date = "2026-10-31"
	if _, err := Run(context.Background(), cfg, in2, Options{Provider: model.NewFake()}); err != nil {
		t.Fatalf("second date: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(work, "digests", "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	idx := string(b)
	if !strings.Contains(idx, "digests/daily/2026-10/2026-10-30.md") || !strings.Contains(idx, "digests/daily/2026-10/2026-10-31.md") {
		t.Fatalf("index links are not daily paths:\n%s", idx)
	}
	if rows := strings.Count(idx, "| 2026-"); rows != 2 {
		t.Fatalf("expected exactly two daily rows, got %d:\n%s", rows, idx)
	}
}

// usageProv wraps another provider and reports a fixed token usage, so the
// app-level usage wiring can be asserted without a real model.
type usageProv struct {
	inner model.Provider
	u     model.Usage
}

func (p usageProv) Name() string { return "usage" }
func (p usageProv) Complete(ctx context.Context, s, u string) (model.Result, error) {
	r, err := p.inner.Complete(ctx, s, u)
	r.Usage = p.u
	return r, err
}

func TestRunRecordsUsage(t *testing.T) {
	cfg := &config.Config{
		Output:   config.OutputConfig{Dir: t.TempDir()},
		Trending: config.TrendingConfig{Since: "daily"},
	}
	prov := usageProv{
		inner: model.NewFake(),
		u:     model.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150, Known: true},
	}
	res, err := Run(context.Background(), cfg, fixtureInputs(), Options{DryRun: true, Provider: prov})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	u := res.Report.Usage
	if u == nil {
		t.Fatal("usage is nil")
	}
	if !u.UsageKnown || u.PromptTokens != 100 || u.CompletionTokens != 50 || u.TotalTokens != 150 {
		t.Fatalf("usage = %+v", u)
	}
	if u.Calls != 1 || u.Batches != 1 {
		t.Fatalf("calls/batches = %d/%d, want 1/1", u.Calls, u.Batches)
	}
}

// captureProvider records the user prompts it was asked to summarize so tests can
// assert that enrich context reached the model.
type captureProvider struct {
	inner   model.Provider
	mu      sync.Mutex
	prompts []string
}

func (p *captureProvider) Name() string { return "capture" }
func (p *captureProvider) Complete(ctx context.Context, system, user string) (model.Result, error) {
	p.mu.Lock()
	p.prompts = append(p.prompts, user)
	p.mu.Unlock()
	return p.inner.Complete(ctx, system, user)
}

// TestRunEnrichInjectsContext wires a fake enrich server end-to-end: the enrich
// stage must fetch topics + README for pending repos, inject TOPICS=/README_EXCERPT=
// into the prompt, and report the enrich stage without failing the run.
func TestRunEnrichInjectsContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/README.md"):
			_, _ = w.Write([]byte("# Title\n\n<b>Widgets</b> for busy teams.\n"))
		default:
			_, _ = w.Write([]byte(`<a href="/topics/ai">ai</a><script>{"defaultBranch":"main"}</script>`))
		}
	}))
	defer srv.Close()

	enr := enrich.New(enrich.Options{
		Mode:     enrich.ModeReadme,
		HTMLBase: srv.URL,
		RawBase:  srv.URL,
		Budget:   5 * time.Second,
		Timeout:  2 * time.Second,
	}, enrich.NewCache())
	cp := &captureProvider{inner: model.NewFake()}
	cfg := &config.Config{
		Output:   config.OutputConfig{Dir: t.TempDir()},
		Trending: config.TrendingConfig{Since: "daily"},
	}
	res, err := Run(context.Background(), cfg, fixtureInputs(), Options{DryRun: true, Provider: cp, Enricher: enr})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Report.Enrich == nil || res.Report.Enrich.Requested != 2 || res.Report.Enrich.Fetched != 2 {
		t.Fatalf("enrich report = %+v", res.Report.Enrich)
	}
	if len(res.Report.Stages) < 2 || res.Report.Stages[0].Name != "enrich" || res.Report.Stages[1].Name != "summarize" {
		t.Fatalf("stages = %+v, want enrich before summarize", res.Report.Stages)
	}
	cp.mu.Lock()
	joined := strings.Join(cp.prompts, "\n---\n")
	cp.mu.Unlock()
	if !strings.Contains(joined, "TOPICS=ai") {
		t.Fatalf("prompt missing TOPICS line:\n%s", joined)
	}
	if !strings.Contains(joined, "README_EXCERPT=") {
		t.Fatalf("prompt missing README_EXCERPT line:\n%s", joined)
	}
	if strings.Contains(joined, "<b>") {
		t.Fatalf("prompt leaked raw HTML from README:\n%s", joined)
	}
}

func TestDigestLanguagesPrefersRequested(t *testing.T) {
	// Requested wins verbatim (order preserved), even if repos imply other langs.
	in := Inputs{
		Repos:     []digest.RepoSnapshot{{FullName: "a/b", Language: "c++"}, {FullName: "a/c", Language: "go"}},
		Requested: []string{"all", "python", "rust"},
	}
	got := digestLanguages(in)
	want := []string{"all", "python", "rust"}
	if !equalStrings(got, want) {
		t.Fatalf("digestLanguages = %v, want %v", got, want)
	}
}

func TestDigestLanguagesFallsBackToRepos(t *testing.T) {
	// No Requested (replay path): fall back to languagesOf, which seeds "all".
	in := Inputs{Repos: []digest.RepoSnapshot{{FullName: "a/b", Language: "go"}}}
	got := digestLanguages(in)
	if len(got) == 0 || got[0] != "all" || !contains(got, "go") {
		t.Fatalf("digestLanguages fallback = %v, want all-prefixed with go", got)
	}
}

func TestDigestByPageCarriesSummaries(t *testing.T) {
	byPage := map[string][]digest.RepoSnapshot{
		"all": {{FullName: "a/one", Language: "c++"}, {FullName: "a/two", Language: "go"}},
	}
	digests := []digest.RepoDigest{
		{RepoSnapshot: digest.RepoSnapshot{FullName: "a/one"}, SummaryZh: "S1"},
		{RepoSnapshot: digest.RepoSnapshot{FullName: "a/two"}, SummaryZh: "S2"},
	}
	got := digestByPage(byPage, digests)
	if got == nil {
		t.Fatal("digestByPage returned nil for non-nil input")
	}
	if len(got["all"]) != 2 || got["all"][0].SummaryZh != "S1" || got["all"][1].SummaryZh != "S2" {
		t.Fatalf("digestByPage did not carry summaries: %+v", got["all"])
	}
	if digestByPage(nil, digests) != nil {
		t.Fatal("nil byPage must stay nil (trigger legacy layout)")
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// The digest bytes must be addressable from the Result itself: the CLI inlines
// them into RunResult (ipc v3) so consumers never read the output repository.
func TestRunExposesDigestPath(t *testing.T) {
	_, _, cfg := setupRepo(t)
	in := fixtureInputs()

	r, err := Run(context.Background(), cfg, in, Options{Provider: model.NewFake()})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	const want = "digests/daily/2026-10/2026-10-30.md"
	if r.DigestPath != want {
		t.Fatalf("DigestPath = %q, want %q", r.DigestPath, want)
	}
	var data []byte
	for _, f := range r.Files {
		if f.Path == want {
			data = f.Data
		}
	}
	if len(data) == 0 {
		t.Fatalf("digest file %q missing from Files", want)
	}
	if !strings.Contains(string(data), "## all") && !strings.Contains(string(data), "## go") {
		t.Fatalf("digest body does not look like markdown:\n%s", data)
	}

	// An unchanged rerun is skipped, but it still rendered the same artifacts:
	// the digest must remain addressable (the plugin refreshes its preview).
	r2, err := Run(context.Background(), cfg, in, Options{Provider: model.NewFake()})
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if !r2.Skipped {
		t.Fatal("second run should be skipped")
	}
	if r2.DigestPath != want {
		t.Fatalf("skipped rerun DigestPath = %q, want %q", r2.DigestPath, want)
	}
}

// Weekly/monthly use different bucket/name rules (ISO year + YYYY-Wnn, YYYY + YYYY-MM);
// the suffix match must keep picking the digest itself, not index.md.
func TestRunExposesDigestPathForWeeklyAndMonthly(t *testing.T) {
	cases := []struct {
		period string
		date   string
		want   string
	}{
		{"weekly", "2026-11-01", "digests/weekly/2026/2026-W44.md"},
		{"monthly", "2026-10-31", "digests/monthly/2026/2026-10.md"},
	}
	for _, tc := range cases {
		_, _, cfg := setupRepo(t)
		in := fixtureInputs()
		in.Date = tc.date
		in.Period = tc.period
		r, err := Run(context.Background(), cfg, in, Options{Provider: model.NewFake()})
		if err != nil {
			t.Fatalf("%s run: %v", tc.period, err)
		}
		if r.DigestPath != tc.want {
			t.Fatalf("%s DigestPath = %q, want %q", tc.period, r.DigestPath, tc.want)
		}
	}
}

// A manual regenerate of a date whose PR was already merged must rebuild the
// branch from base, not append to the spent branch: main holds a squash-merged
// copy, so appending keeps the old base and the new PR can never merge.
func TestManualRebuildResetsSpentBranchToBase(t *testing.T) {
	remote, _, cfg := setupRepo(t)
	cfg.PR = config.PRConfig{Enabled: true, AutoMerge: false}

	first, err := Run(context.Background(), cfg, fixtureInputs(), Options{Provider: model.NewFake()})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if first.Commit == "" {
		t.Fatal("first run did not commit")
	}
	oldCommit := first.Commit

	mergedAt := time.Now()
	created := &pr.Pull{Number: 9, HTMLURL: "https://github.com/acme/widgets/pull/9"}
	fake := &fakePR{
		latest:  &pr.Pull{Number: 5, State: "closed", MergedAt: &mergedAt},
		created: created,
	}
	withPRClient(t, fake)

	in := fixtureInputs()
	in.Repos = append(in.Repos, digest.RepoSnapshot{
		FullName: "acme/three", URL: "https://github.com/acme/three", Description: digest.StrPtr("third"),
		Language: "go", Stars: 300, StarsInPeriod: 30, Forks: digest.IntPtr(6), Rank: 3, Source: digest.SourceTrendingHTML,
	})
	r, err := Run(context.Background(), cfg, in, Options{Provider: model.NewFake(), Token: "tok", Trigger: TriggerManual})
	if err != nil {
		t.Fatalf("manual rebuild: %v", err)
	}
	if r.Skipped {
		t.Fatalf("manual rebuild must not skip, got %q", r.SkipReason)
	}
	if fake.creates != 1 || r.PRURL != created.HTMLURL {
		t.Fatalf("expected a fresh PR, creates=%d url=%q", fake.creates, r.PRURL)
	}
	// The spent branch's commit must be gone: the rebuilt branch starts at base.
	if isAncestor(t, remote, oldCommit, "refs/heads/trending/daily/2026-10-30") {
		t.Fatal("rebuilt branch still contains the old (spent) commit; it was appended, not reset")
	}
}

// An open PR's branch is still in flight: a manual rerun keeps appending so the
// PR history (and any human review context) is preserved.
func TestManualRerunKeepsAppendingWhenPROpen(t *testing.T) {
	remote, _, cfg := setupRepo(t)
	cfg.PR = config.PRConfig{Enabled: true, AutoMerge: false}

	first, err := Run(context.Background(), cfg, fixtureInputs(), Options{Provider: model.NewFake()})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	before := countRef(t, remote, "trending/daily/2026-10-30")

	open := &pr.Pull{Number: 7, State: "open", HTMLURL: "https://github.com/acme/widgets/pull/7"}
	withPRClient(t, &fakePR{latest: open, existing: open})
	in := fixtureInputs()
	in.Repos = append(in.Repos, digest.RepoSnapshot{
		FullName: "acme/three", URL: "https://github.com/acme/three", Description: digest.StrPtr("third"),
		Language: "go", Stars: 300, StarsInPeriod: 30, Forks: digest.IntPtr(6), Rank: 3, Source: digest.SourceTrendingHTML,
	})
	if _, err := Run(context.Background(), cfg, in, Options{Provider: model.NewFake(), Token: "tok", Trigger: TriggerManual}); err != nil {
		t.Fatalf("manual rerun: %v", err)
	}
	if after := countRef(t, remote, "trending/daily/2026-10-30"); after != before+1 {
		t.Fatalf("open PR branch should append one commit: before=%d after=%d", before, after)
	}
	if !isAncestor(t, remote, first.Commit, "refs/heads/trending/daily/2026-10-30") {
		t.Fatal("open PR branch lost its previous commit")
	}
}

// Regression: the release stage used to overwrite the trending digest's
// date/period with the release document's week end, which leaked into the PR
// title ("monthly" runs titled as weekly PRs for the following Sunday) and into
// Result.Digest.
func TestReleaseStageDoesNotRewriteTrendingDigest(t *testing.T) {
	_, _, cfg := setupRepo(t)
	cfg.Releases = config.ReleasesConfig{Enabled: true, Period: "weekly", Timezone: "Asia/Shanghai"}
	dir := t.TempDir()
	r, err := Run(context.Background(), cfg, fixtureInputs(), Options{
		Provider: model.NewFake(),
		Releases: &ReleasesOptions{
			Repos:     []string{"acme/one"},
			Fetcher:   emptyReleaseFetcher{},
			StatePath: filepath.Join(dir, "release-state.json"),
			CachePath: filepath.Join(dir, "release-cache.json"),
		},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if r.Digest.Date != "2026-10-30" || r.Digest.Period != "daily" {
		t.Fatalf("release stage rewrote the trending digest: date=%q period=%q", r.Digest.Date, r.Digest.Period)
	}
}

// emptyReleaseFetcher reports no releases/tags; it only exists so the release
// stage runs (and used to overwrite the digest) without touching the network.
type emptyReleaseFetcher struct{}

func (emptyReleaseFetcher) ListReleasesPage(context.Context, string, string, int, int) (fetch.ReleasePage, error) {
	return fetch.ReleasePage{}, nil
}

func (emptyReleaseFetcher) ListTagsPage(context.Context, string, string, int, int) (fetch.TagPage, error) {
	return fetch.TagPage{}, nil
}

func (emptyReleaseFetcher) GetCommit(context.Context, string, string, string) (fetch.Commit, error) {
	return fetch.Commit{}, nil
}

// isAncestor reports whether commit is reachable from ref in the bare remote.
func isAncestor(t *testing.T, remote, commit, ref string) bool {
	t.Helper()
	cmd := exec.Command("git", "--git-dir", remote, "merge-base", "--is-ancestor", commit, ref)
	return cmd.Run() == nil
}
