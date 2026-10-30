package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/morehao/trendradar/internal/ipc"
)

// Toplevel verifies the directory is inside a git work tree.
func (g *Git) Toplevel(ctx context.Context) (string, error) {
	out, _, err := g.run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", &Error{Code: ipc.CodeRepoNotGit, Message: "not a git repository", Err: err}
	}
	return strings.TrimSpace(out), nil
}

// IsClean reports whether the tracked working tree has no changes. Untracked
// files are ignored (the renderer's new files are untracked until `add`).
func (g *Git) IsClean(ctx context.Context) (bool, error) {
	out, _, err := g.run(ctx, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// Fetch updates remote refs.
func (g *Git) Fetch(ctx context.Context, remote string) error {
	_, _, err := g.run(ctx, "fetch", remote)
	return err
}

// CurrentBranch returns the current branch name.
func (g *Git) CurrentBranch(ctx context.Context) (string, error) {
	out, _, err := g.run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// SwitchCreate force-creates (or resets) branch at startPoint.
func (g *Git) SwitchCreate(ctx context.Context, branch, startPoint string) error {
	_, _, err := g.run(ctx, "switch", "-C", branch, startPoint)
	return err
}

// AddAll stages everything under path (relative to the repo dir).
func (g *Git) AddAll(ctx context.Context, path string) error {
	_, _, err := g.run(ctx, "add", "-A", "--", path)
	return err
}

// HasStagedChanges reports whether the index differs from HEAD.
func (g *Git) HasStagedChanges(ctx context.Context) (bool, error) {
	_, _, err := g.run(ctx, "diff", "--cached", "--quiet")
	if err == nil {
		return false, nil
	}
	var ge *Error
	if errors.As(err, &ge) {
		var ee *exec.ExitError
		if errors.As(ge.Err, &ee) && ee.ExitCode() == 1 {
			return true, nil
		}
	}
	return false, err
}

// Commit commits staged changes with the given author identity.
func (g *Git) Commit(ctx context.Context, message, name, email string) error {
	args := make([]string, 0, 6)
	if name != "" {
		args = append(args, "-c", "user.name="+name)
	}
	if email != "" {
		args = append(args, "-c", "user.email="+email)
	}
	args = append(args, "commit", "-m", message)
	_, _, err := g.run(ctx, args...)
	return err
}

// Push pushes branch to remote (fast-forward only). When token is non-empty it
// is fed through a temporary GIT_ASKPASS helper (0600, removed after), never via
// argv.
func (g *Git) Push(ctx context.Context, remote, branch, token string) error {
	return g.push(ctx, []string{"push", "-u", remote, branch}, token)
}

// PushForceWithLease pushes branch with --force-with-lease. It is meant for the
// automation's own per-date branches, which are rebuilt from the base branch on
// every run: a rebuilt branch is not a descendant of the previous push, so a
// plain push would be rejected as non-fast-forward. The lease keeps this safe by
// only overwriting the remote branch if it still points where our last fetch saw
// it; if someone else pushed in between, the push is refused instead of
// clobbering their commits. The caller must fetch first so the lease baseline is
// current.
func (g *Git) PushForceWithLease(ctx context.Context, remote, branch, token string) error {
	return g.push(ctx, []string{"push", "--force-with-lease", "-u", remote, branch}, token)
}

func (g *Git) push(ctx context.Context, args []string, token string) error {
	if token == "" {
		_, _, err := g.run(ctx, args...)
		return err
	}
	script, cleanup, err := writeAskpass(token)
	if err != nil {
		return err
	}
	defer cleanup()
	env := []string{
		"GIT_ASKPASS=" + script,
		"GIT_TERMINAL_PROMPT=0",
		"TRENDRADAR_GIT_TOKEN=" + token,
	}
	_, _, err = g.runEnv(ctx, env, args...)
	return err
}

// IsOwnedBranch reports whether branch belongs to the automation and may
// therefore be force-pushed. Ownership is defined by the configured output
// branch prefix (default "trending/"); everything else must use a plain push so
// a misconfiguration can never force over a human branch.
func IsOwnedBranch(branch, prefix string) bool {
	if prefix == "" {
		prefix = "trending/"
	}
	return strings.HasPrefix(branch, prefix)
}

// PullRebase rebases the current branch onto the remote branch.
func (g *Git) PullRebase(ctx context.Context, remote, branch string) error {
	_, _, err := g.run(ctx, "pull", "--rebase", remote, branch)
	return err
}

func writeAskpass(token string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "trendradar-askpass-*")
	if err != nil {
		return "", func() {}, err
	}
	script := filepath.Join(dir, "askpass.sh")
	body := "#!/bin/sh\ncase \"$1\" in\n  *Username*) echo \"x-access-token\" ;;\n  *) echo \"$TRENDRADAR_GIT_TOKEN\" ;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	return script, cleanup, nil
}

// RemoteURL returns the URL configured for a remote.
func (g *Git) RemoteURL(ctx context.Context, remote string) (string, error) {
	out, _, err := g.run(ctx, "remote", "get-url", remote)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
