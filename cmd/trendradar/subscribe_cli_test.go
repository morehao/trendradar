package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cliGit runs git in dir and fails the test on error.
func cliGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// setupSubscribeRepo builds a data repo + a config.yaml pointing at it, returning
// the config path and the repo work dir.
func setupSubscribeRepo(t *testing.T) (cfgPath, work string) {
	t.Helper()
	root := t.TempDir()
	work = filepath.Join(root, "work")
	cliGit(t, "", "init", "-b", "main", work)
	cliGit(t, work, "config", "user.name", "tester")
	cliGit(t, work, "config", "user.email", "tester@example.com")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cliGit(t, work, "add", "-A")
	cliGit(t, work, "commit", "-m", "init")

	cfgPath = filepath.Join(root, "config.yaml")
	cfg := "model:\n  provider: fake\n  name: deepseek-flash\n  baseURL: https://api.deepseek.com/v1\n" +
		"repo:\n  path: " + work + "\n  remote: origin\n  baseBranch: main\n" +
		"trending:\n  languages: [all]\n  since: daily\n" +
		"output:\n  dir: digests\n  branchPrefix: trending/\n" +
		"releases:\n  enabled: true\n  subscriptionsFile: subscriptions.yaml\n  period: daily\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfgPath, work
}

func gitStatusPorcelain(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=no")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// G6: a subscribe add/remove writes only .git/pending, so git status stays clean
// (IsClean never trips REPO_DIRTY from the second cycle on).
func TestSubscribeAddKeepsWorktreeClean(t *testing.T) {
	cfgPath, work := setupSubscribeRepo(t)

	if err := runSubscribe([]string{"add", "o/r", "--config", cfgPath}); err != nil {
		t.Fatalf("subscribe add: %v", err)
	}
	if got := gitStatusPorcelain(t, work); got != "" {
		t.Fatalf("worktree dirty after add:\n%s", got)
	}
	// pending exists under .git
	if _, err := os.Stat(filepath.Join(work, ".git", "trendradar-subscriptions.pending")); err != nil {
		t.Fatalf("pending file not written: %v", err)
	}

	// add a second repo, then list reflects both via union.
	if err := runSubscribe([]string{"add", "o/two", "--config", cfgPath}); err != nil {
		t.Fatalf("second add: %v", err)
	}
	if got := gitStatusPorcelain(t, work); got != "" {
		t.Fatalf("worktree dirty after second add:\n%s", got)
	}
}

// --file must be rejected on add/remove (read-only escape hatch only).
func TestSubscribeFileRejectedOnWrite(t *testing.T) {
	cfgPath, _ := setupSubscribeRepo(t)
	err := runSubscribe([]string{"add", "o/r", "--config", cfgPath, "--file", "x.yaml"})
	if err == nil {
		t.Fatal("expected error for --file on add")
	}
	if code := classifySubscribeError(err); code != "CONFIG_INVALID" {
		t.Fatalf("code = %s, want CONFIG_INVALID", code)
	}
}

// remove of a repo that is not subscribed is a caller error.
func TestSubscribeRemoveMissing(t *testing.T) {
	cfgPath, _ := setupSubscribeRepo(t)
	if err := runSubscribe([]string{"add", "o/r", "--config", cfgPath}); err != nil {
		t.Fatal(err)
	}
	if err := runSubscribe([]string{"remove", "o/other", "--config", cfgPath}); err == nil {
		t.Fatal("expected error removing a non-subscribed repo")
	} else if classifySubscribeError(err) != "CONFIG_INVALID" {
		t.Fatalf("code = %s, want CONFIG_INVALID", classifySubscribeError(err))
	}
	// The valid repo is still in pending.
	if err := runSubscribe([]string{"remove", "o/r", "--config", cfgPath}); err != nil {
		t.Fatalf("remove existing: %v", err)
	}
}

// subscribe without --config is a caller error (cannot guess the data repo).
func TestSubscribeRequiresConfig(t *testing.T) {
	if err := runSubscribe([]string{"add", "o/r"}); err == nil {
		t.Fatal("expected error without --config")
	} else if classifySubscribeError(err) != "CONFIG_INVALID" {
		t.Fatalf("code = %s, want CONFIG_INVALID", classifySubscribeError(err))
	}
}
