// Package gitx wraps the system `git` binary. It never shells out through a
// shell and never puts a token in argv.
package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/morehao/trendradar/internal/ipc"
)

// Error carries a stable IPC code for git failures.
type Error struct {
	Code    ipc.Code
	Message string
	// Details is machine-readable context for the consumer (stage, reason,
	// branch, remote). It never contains secrets and never changes the code.
	Details map[string]any
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

// Git runs git commands in Dir with a 30s timeout.
type Git struct {
	Dir     string
	Timeout time.Duration
}

// New returns a Git bound to dir.
func New(dir string) *Git { return &Git{Dir: dir, Timeout: 30 * time.Second} }

// EnsureInstalled returns GIT_NOT_INSTALLED when git is missing.
func EnsureInstalled() error {
	if _, err := exec.LookPath("git"); err != nil {
		return &Error{Code: ipc.CodeGitNotInstalled, Message: "git not found in PATH", Err: err}
	}
	return nil
}

func (g *Git) run(ctx context.Context, args ...string) (string, string, error) {
	return g.runEnv(ctx, nil, args...)
}

func (g *Git) runEnv(ctx context.Context, extraEnv []string, args ...string) (string, string, error) {
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "git", args...)
	cmd.Dir = g.Dir
	cmd.Env = append(os.Environ(), extraEnv...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb

	err := cmd.Run()
	stdout, stderr := out.String(), errb.String()
	if err != nil {
		return stdout, stderr, &Error{Code: ipc.CodeGitOperationFailed, Message: strings.TrimSpace(stderr), Err: err}
	}
	return stdout, stderr, nil
}
