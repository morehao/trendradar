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

func TestIsOwnedBranch(t *testing.T) {
	cases := []struct {
		branch, prefix string
		want           bool
	}{
		{"trending/2026-10-31", "trending/", true},
		{"trending/2026-10-31", "", true}, // empty prefix -> default "trending/"
		{"bot/2026-10-31", "bot/", true},
		{"main", "trending/", false},
		{"feature/x", "trending/", false},
		{"release/v1", "trending/", false},
	}
	for _, c := range cases {
		if got := IsOwnedBranch(c.branch, c.prefix); got != c.want {
			t.Errorf("IsOwnedBranch(%q, %q) = %v, want %v", c.branch, c.prefix, got, c.want)
		}
	}
}

// C1: a branch rebuilt from base is not a descendant of the previous push, so a
// plain push fails; force-with-lease must replace the remote branch cleanly.
func TestPushForceWithLeaseReplacesOwnBranch(t *testing.T) {
	remote, work := setupRepoPair(t)
	ctx := context.Background()
	g := New(work)

	gitRun(t, work, "switch", "-c", "trending/2026-10-31", "origin/main")
	commitFile(t, work, "digests/2026-10-31.md", "A\n", "A")
	if err := g.Push(ctx, "origin", "trending/2026-10-31", ""); err != nil {
		t.Fatalf("initial push: %v", err)
	}
	first := refSHA(t, remote, "trending/2026-10-31")

	// Simulate a same-date rerun: fetch, rebuild the branch from origin/main and
	// commit different content, then push again.
	if err := g.Fetch(ctx, "origin"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	gitRun(t, work, "switch", "-C", "trending/2026-10-31", "origin/main")
	commitFile(t, work, "digests/2026-10-31.md", "B\n", "B")
	if err := g.PushForceWithLease(ctx, "origin", "trending/2026-10-31", ""); err != nil {
		t.Fatalf("force-with-lease push: %v", err)
	}
	second := refSHA(t, remote, "trending/2026-10-31")
	if second == first {
		t.Fatal("remote branch was not replaced")
	}
	if got, want := refCount(t, remote, "trending/2026-10-31"), refCount(t, remote, "main")+1; got != want {
		t.Fatalf("branch commits = %d, want %d (force replaces, not stacks)", got, want)
	}
}

// C1 safety: force-with-lease must refuse to overwrite commits pushed by someone
// else after our last fetch.
func TestPushForceWithLeaseRefusesWhenRemoteMoved(t *testing.T) {
	remote, work := setupRepoPair(t)
	ctx := context.Background()
	g := New(work)

	gitRun(t, work, "switch", "-c", "trending/2026-10-31", "origin/main")
	commitFile(t, work, "digests/2026-10-31.md", "A\n", "A")
	if err := g.Push(ctx, "origin", "trending/2026-10-31", ""); err != nil {
		t.Fatalf("initial push: %v", err)
	}

	// A different actor pushes another commit to the same branch.
	other := filepath.Join(t.TempDir(), "other")
	gitRun(t, "", "clone", remote, other)
	gitRun(t, other, "config", "user.name", "other")
	gitRun(t, other, "config", "user.email", "other@example.com")
	gitRun(t, other, "switch", "trending/2026-10-31")
	commitFile(t, other, "digests/2026-10-31.md", "theirs\n", "theirs")
	gitRun(t, other, "push", "origin", "trending/2026-10-31")
	theirs := refSHA(t, remote, "trending/2026-10-31")

	// Local rebuild WITHOUT fetching: our remote-tracking ref still points at the
	// old commit, so the lease no longer matches the (moved) remote.
	gitRun(t, work, "switch", "-C", "trending/2026-10-31", "origin/main")
	commitFile(t, work, "digests/2026-10-31.md", "ours\n", "ours")
	if err := g.PushForceWithLease(ctx, "origin", "trending/2026-10-31", ""); err == nil {
		t.Fatal("force-with-lease must be rejected when the remote branch moved after our fetch")
	}
	if got := refSHA(t, remote, "trending/2026-10-31"); got != theirs {
		t.Fatalf("remote branch was clobbered: got %s, want %s", got, theirs)
	}
}
