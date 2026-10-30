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
