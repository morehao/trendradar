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
//
// The automation never force-pushes: per-date branches are append-only, so a
// rejected push means the remote moved and the caller should fetch + rebase +
// retry once (see app.pushWithRetry).
func (g *Git) Push(ctx context.Context, remote, branch, token string) error {
	return g.push(ctx, []string{"push", "-u", remote, branch}, token)
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

// HasRef reports whether a fully qualified ref exists locally (e.g.
// refs/heads/x or refs/remotes/origin/x). A missing ref is not an error.
func (g *Git) HasRef(ctx context.Context, ref string) (bool, error) {
	_, _, err := g.run(ctx, "rev-parse", "--verify", "--quiet", ref)
	if err == nil {
		return true, nil
	}
	var ge *Error
	if errors.As(err, &ge) {
		var ee *exec.ExitError
		if errors.As(ge.Err, &ee) && ee.ExitCode() == 1 {
			return false, nil
		}
	}
	return false, err
}

// Switch checks out an existing branch without resetting it.
func (g *Git) Switch(ctx context.Context, branch string) error {
	_, _, err := g.run(ctx, "switch", branch)
	return err
}

// HeadSHA returns the current HEAD commit.
func (g *Git) HeadSHA(ctx context.Context) (string, error) {
	out, _, err := g.run(ctx, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// PushRejected reports whether a push failed because the remote moved (the only
// case worth fetch+rebase+retry). Auth/protected/hook failures are not retried.
func PushRejected(err error) bool {
	var ge *Error
	if !errors.As(err, &ge) {
		return false
	}
	msg := ge.Message
	if strings.Contains(msg, "stale info") {
		return false
	}
	return strings.Contains(msg, "non-fast-forward") ||
		strings.Contains(msg, "fetch first") ||
		strings.Contains(msg, "[rejected]")
}

// ClassifyPushFailure turns git's stderr into a stable reason the consumer can
// map to localized copy. It never changes the error code.
func ClassifyPushFailure(stderr string) string {
	switch {
	case strings.Contains(stderr, "stale info"):
		// Force-with-lease only; kept for old branches that still force.
		return "stale-lease"
	case strings.Contains(stderr, "non-fast-forward") || strings.Contains(stderr, "fetch first"):
		return "non-fast-forward"
	case strings.Contains(stderr, "Permission denied"),
		strings.Contains(stderr, "Authentication failed"),
		strings.Contains(stderr, "could not read Username"),
		strings.Contains(stderr, "Invalid username or password"):
		return "auth"
	case strings.Contains(stderr, "protected branch"), strings.Contains(stderr, "GH006"):
		return "protected"
	case strings.Contains(stderr, "hook declined"), strings.Contains(stderr, "pre-receive hook"):
		return "hook"
	case strings.Contains(stderr, "pathspec"):
		return "pathspec"
	default:
		return "unknown"
	}
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
