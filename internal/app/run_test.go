package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/morehao/trendradar/internal/config"
	"github.com/morehao/trendradar/internal/digest"
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
		return digest.RepoSnapshot{FullName: name, URL: "https://github.com/" + name, Description: "desc", Language: "go", Stars: 100 + i, StarsInPeriod: 10, Forks: 5, Rank: i + 1, Source: digest.SourceTrendingHTML}
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
	if r1.Branch != "trending/2026-10-30" {
		t.Fatalf("branch = %q", r1.Branch)
	}
	base := countRef(t, remote, "main")
	if got := countRef(t, remote, "trending/2026-10-30"); got != base+1 {
		t.Fatalf("remote branch commits = %d, want %d", got, base+1)
	}

	r2, err := Run(context.Background(), cfg, in, Options{Provider: model.NewFake()})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !r2.Skipped {
		t.Fatal("second run should be skipped (same contentHash)")
	}
	if got := countRef(t, remote, "trending/2026-10-30"); got != base+1 {
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
	if r.Branch != "trending/2026-10-31" {
		t.Fatalf("branch = %q", r.Branch)
	}
	base := countRef(t, remote, "main")
	if got := countRef(t, remote, "trending/2026-10-31"); got != base+1 {
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
	if r.Branch != "trending/2026-10-30" {
		t.Fatalf("branch = %q", r.Branch)
	}
	base := countRef(t, remote, "main")
	if got := countRef(t, remote, "trending/2026-10-30"); got != base+1 {
		t.Fatalf("branch commits = %d, want %d", got, base+1)
	}
	for _, rel := range []string{"trending/2026-10-30/2026-10-30.md", "trending/2026-10-30/2026-10-30.json", "trending/index.md"} {
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
	md, err := os.ReadFile(filepath.Join(work, "digests", "2026-10-30", "2026-10-30.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "降级") {
		t.Fatalf("markdown is missing the degraded notice:\n%s", md)
	}
}

type fakePR struct {
	existing *pr.Pull
	created  *pr.Pull
	merged   []int
}

func (f *fakePR) FindOpen(context.Context, string, string) (*pr.Pull, error) { return f.existing, nil }
func (f *fakePR) Create(context.Context, string, string, string, string) (*pr.Pull, error) {
	return f.created, nil
}
func (f *fakePR) Update(context.Context, int, string, string) (*pr.Pull, error) {
	return f.existing, nil
}
func (f *fakePR) Merge(_ context.Context, n int) error {
	f.merged = append(f.merged, n)
	return nil
}

func newOpenPRTestGit(t *testing.T) *gitx.Git {
	t.Helper()
	work := t.TempDir()
	runGit(t, "", "init", work)
	runGit(t, work, "remote", "add", "origin", "https://github.com/acme/widgets.git")
	return gitx.New(work)
}

// Regression: autoMerge must apply to a freshly created PR, not only to a PR
// that already existed on a previous run.
func TestOpenPRAutoMergesNewPR(t *testing.T) {
	fake := &fakePR{created: &pr.Pull{Number: 11, HTMLURL: "https://github.com/acme/widgets/pull/11"}}
	old := newPRClient
	newPRClient = func(string, string, string) prAPI { return fake }
	defer func() { newPRClient = old }()

	cfg := &config.Config{
		Repo: config.RepoConfig{Remote: "origin", BaseBranch: "main"},
		PR:   config.PRConfig{Enabled: true, AutoMerge: true},
	}
	url, err := openPR(context.Background(), newOpenPRTestGit(t), cfg, "tok", "trending/2026-10-30", digest.Digest{Date: "2026-10-30", Count: 3})
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
	old := newPRClient
	newPRClient = func(string, string, string) prAPI { return fake }
	defer func() { newPRClient = old }()

	cfg := &config.Config{
		Repo: config.RepoConfig{Remote: "origin", BaseBranch: "main"},
		PR:   config.PRConfig{Enabled: true, AutoMerge: true},
	}
	if _, err := openPR(context.Background(), newOpenPRTestGit(t), cfg, "tok", "trending/2026-10-30", digest.Digest{Date: "2026-10-30", Count: 1}); err != nil {
		t.Fatalf("openPR: %v", err)
	}
	if len(fake.merged) != 1 || fake.merged[0] != 9 {
		t.Fatalf("existing PR merge = %v", fake.merged)
	}
}
