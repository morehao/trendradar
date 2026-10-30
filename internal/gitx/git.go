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

// Push pushes branch to remote. When token is non-empty it is fed through a
// temporary GIT_ASKPASS helper (0600, removed after), never via argv.
func (g *Git) Push(ctx context.Context, remote, branch, token string) error {
	args := []string{"push", "-u", remote, branch}
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
