package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (dir %q): %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func commitFile(t *testing.T, dir, name, content, msg string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", msg)
}

func refSHA(t *testing.T, gitDir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", gitDir, "rev-parse", ref).Output()
	if err != nil {
		t.Fatalf("rev-parse %s: %v", ref, err)
	}
	return strings.TrimSpace(string(out))
}

func refCount(t *testing.T, gitDir, ref string) int {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", gitDir, "rev-list", "--count", ref).Output()
	if err != nil {
		t.Fatalf("rev-list %s: %v", ref, err)
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n
}

// setupRepoPair creates a bare remote and a work tree pushed to main.
func setupRepoPair(t *testing.T) (remote, work string) {
	t.Helper()
	root := t.TempDir()
	remote = filepath.Join(root, "remote.git")
	work = filepath.Join(root, "work")
	gitRun(t, "", "init", "--bare", "-b", "main", remote)
	gitRun(t, "", "init", "-b", "main", work)
	gitRun(t, work, "config", "user.name", "tester")
	gitRun(t, work, "config", "user.email", "tester@example.com")
	commitFile(t, work, "README.md", "# init\n", "init")
	gitRun(t, work, "remote", "add", "origin", remote)
	gitRun(t, work, "push", "-u", "origin", "main")
	return remote, work
}

// The automation never rewrites a branch: a rerun appends a commit, so a plain
// push stays a fast-forward.
func TestPushAppendsOnExistingBranch(t *testing.T) {
	remote, work := setupRepoPair(t)
	ctx := context.Background()
	g := New(work)

	gitRun(t, work, "switch", "-c", "trending/2026-10-31", "origin/main")
	commitFile(t, work, "digests/2026-10-31.md", "A\n", "A")
	if err := g.Push(ctx, "origin", "trending/2026-10-31", ""); err != nil {
		t.Fatalf("initial push: %v", err)
	}
	base := refCount(t, remote, "main")

	commitFile(t, work, "digests/2026-10-31.md", "B\n", "B")
	if err := g.Push(ctx, "origin", "trending/2026-10-31", ""); err != nil {
		t.Fatalf("append push: %v", err)
	}
	if got, want := refCount(t, remote, "trending/2026-10-31"), base+2; got != want {
		t.Fatalf("branch commits = %d, want %d (append, not replace)", got, want)
	}
}

// A push rejected because the remote moved must be recognizable, so the caller
// can fetch+rebase+retry instead of surfacing a dead end.
func TestPushRejectedWhenRemoteMoved(t *testing.T) {
	remote, work := setupRepoPair(t)
	ctx := context.Background()
	g := New(work)

	gitRun(t, work, "switch", "-c", "trending/2026-10-31", "origin/main")
	commitFile(t, work, "digests/2026-10-31.md", "A\n", "A")
	if err := g.Push(ctx, "origin", "trending/2026-10-31", ""); err != nil {
		t.Fatalf("initial push: %v", err)
	}
	base := refCount(t, remote, "main")

	// Another actor appends first.
	other := filepath.Join(t.TempDir(), "other")
	gitRun(t, "", "clone", remote, other)
	gitRun(t, other, "config", "user.name", "other")
	gitRun(t, other, "config", "user.email", "other@example.com")
	gitRun(t, other, "switch", "trending/2026-10-31")
	commitFile(t, other, "digests/2026-10-31-notes.md", "theirs\n", "theirs")
	gitRun(t, other, "push", "origin", "trending/2026-10-31")

	commitFile(t, work, "digests/2026-10-31.md", "ours\n", "ours")
	err := g.Push(ctx, "origin", "trending/2026-10-31", "")
	if err == nil {
		t.Fatal("push must be rejected after the remote moved")
	}
	if !PushRejected(err) {
		t.Fatalf("PushRejected = false for %v", err)
	}

	// The documented recovery path: fetch, rebase onto the remote, push again.
	if err := g.Fetch(ctx, "origin"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if err := g.PullRebase(ctx, "origin", "trending/2026-10-31"); err != nil {
		t.Fatalf("pull --rebase: %v", err)
	}
	if err := g.Push(ctx, "origin", "trending/2026-10-31", ""); err != nil {
		t.Fatalf("push after rebase: %v", err)
	}
	if got, want := refCount(t, remote, "trending/2026-10-31"), base+3; got != want {
		t.Fatalf("branch commits = %d, want %d (both sides preserved)", got, want)
	}
}

func TestClassifyPushFailure(t *testing.T) {
	cases := []struct {
		stderr string
		want   string
	}{
		{"! [rejected] trending/x -> trending/x (non-fast-forward)", "non-fast-forward"},
		{"! [rejected] trending/x -> trending/x (stale info)", "stale-lease"},
		{"git@github.com: Permission denied (publickey).", "auth"},
		{"remote: error: GH006: Protected branch update failed", "protected"},
		{"remote: error: hook declined to update refs/heads/x", "hook"},
		{"error: pathspec 'trending' did not match any files", "pathspec"},
		{"fatal: something unexpected", "unknown"},
	}
	for _, c := range cases {
		if got := ClassifyPushFailure(c.stderr); got != c.want {
			t.Errorf("ClassifyPushFailure(%q) = %q, want %q", c.stderr, got, c.want)
		}
	}
}

func TestHasRef(t *testing.T) {
	_, work := setupRepoPair(t)
	ctx := context.Background()
	g := New(work)

	if ok, err := g.HasRef(ctx, "refs/heads/main"); err != nil || !ok {
		t.Fatalf("refs/heads/main = %v, %v", ok, err)
	}
	if ok, err := g.HasRef(ctx, "refs/heads/nope"); err != nil || ok {
		t.Fatalf("missing ref = %v, %v (err must be nil)", ok, err)
	}
	if err := g.Fetch(ctx, "origin"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if ok, err := g.HasRef(ctx, "refs/remotes/origin/main"); err != nil || !ok {
		t.Fatalf("refs/remotes/origin/main = %v, %v", ok, err)
	}
}
