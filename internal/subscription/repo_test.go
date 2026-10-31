package subscription

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repoGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// initRepoWithBranch creates a git repo with a single commit on main.
func initRepoWithBranch(t *testing.T) (work string) {
	t.Helper()
	work = t.TempDir()
	repoGit(t, work, "init", "-b", "main")
	repoGit(t, work, "config", "user.name", "tester")
	repoGit(t, work, "config", "user.email", "tester@example.com")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repoGit(t, work, "add", "-A")
	repoGit(t, work, "commit", "-m", "init")
	return work
}

func TestRepoSubscriptionsPath(t *testing.T) {
	repo := "/data/repo"
	cases := []struct {
		name      string
		outputDir string
		file      string
		want      string
		wantErr   bool
	}{
		{"default empty dir+file", "", "", "/data/repo/digests/subscriptions.yaml", false},
		{"empty dir explicit file", "", "subscriptions.yaml", "/data/repo/digests/subscriptions.yaml", false},
		{"custom dir", "trending", "subs.yaml", "/data/repo/trending/subs.yaml", false},
		{"dir with . fallback", ".", "subscriptions.yaml", "/data/repo/digests/subscriptions.yaml", false},
		{"file in subdir", "digests", "cfg/subs.yaml", "/data/repo/digests/cfg/subs.yaml", false},
		{"absolute file rejected", "digests", "/etc/subs.yaml", "", true},
		{"dotdot escape rejected", "digests", "../subs.yaml", "", true},
		{"deep dotdot escape rejected", "digests", "a/../../subs.yaml", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := RepoSubscriptionsPath(repo, c.outputDir, c.file)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestRunBranchName(t *testing.T) {
	cases := []struct {
		prefix, period, date, want string
	}{
		{"", "daily", "2026-10-30", "trending/daily/2026-10-30"},
		{"trending/", "weekly", "2026-11-01", "trending/weekly/2026-W44"},
		{"custom/", "monthly", "2026-11-15", "custom/monthly/2026-11"},
		{"", "bogus", "2026-10-30", "trending/daily/2026-10-30"}, // period normalised to daily
	}
	for _, c := range cases {
		if got := RunBranchName(c.prefix, c.period, c.date); got != c.want {
			t.Errorf("RunBranchName(%q,%q,%q) = %q, want %q", c.prefix, c.period, c.date, got, c.want)
		}
	}
}

func TestMergeUnique(t *testing.T) {
	cases := []struct {
		name       string
		a, b       []string
		wantMerged []string
		wantAdded  int
	}{
		{"empty both", nil, nil, []string{}, 0},
		{"a only", []string{"x/y"}, nil, []string{"x/y"}, 0},
		{"b only", nil, []string{"x/y"}, []string{"x/y"}, 1},
		{"union preserved order", []string{"a/b", "c/d"}, []string{"c/d", "e/f"}, []string{"a/b", "c/d", "e/f"}, 1},
		{"all dup", []string{"a/b"}, []string{"a/b"}, []string{"a/b"}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, added := MergeUnique(c.a, c.b)
			if added != c.wantAdded {
				t.Errorf("added = %d, want %d", added, c.wantAdded)
			}
			if strings.Join(got, ",") != strings.Join(c.wantMerged, ",") {
				t.Errorf("merged = %v, want %v", got, c.wantMerged)
			}
		})
	}
}

// Pending round-trip: write then read; and empty write removes the file.
func TestPendingRoundTrip(t *testing.T) {
	work := initRepoWithBranch(t)
	if _, ok, err := ReadPending(work); err != nil || ok {
		t.Fatalf("initial ReadPending = ok=%v err=%v, want false,nil", ok, err)
	}
	if err := WritePending(work, []string{"a/b", "c/d"}); err != nil {
		t.Fatalf("WritePending: %v", err)
	}
	got, ok, err := ReadPending(work)
	if err != nil || !ok {
		t.Fatalf("ReadPending ok=%v err=%v", ok, err)
	}
	if strings.Join(got, ",") != "a/b,c/d" {
		t.Fatalf("pending = %v", got)
	}
	// The pending file must NOT show up in git status (it lives under .git/).
	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=all")
	cmd.Dir = work
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("pending polluted git status:\n%s", out)
	}
	// clear
	if err := ClearPending(work); err != nil {
		t.Fatalf("ClearPending: %v", err)
	}
	if _, ok, _ := ReadPending(work); ok {
		t.Fatal("pending still present after clear")
	}
}

// WritePending with an empty slice removes any existing pending file.
func TestWritePendingEmptyRemoves(t *testing.T) {
	work := initRepoWithBranch(t)
	if err := WritePending(work, []string{"a/b"}); err != nil {
		t.Fatal(err)
	}
	if err := WritePending(work, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := ReadPending(work); ok {
		t.Fatal("empty WritePending should remove the pending file")
	}
}

// Closest empirical scenario: branch has a subscription file, plus pending that
// adds a repo. ReadRepoOnTargetBranch must read the branch's file (not the
// checked-out branch's), and MergeUnique must union them.
func TestReadRepoOnTargetBranchAndMerge(t *testing.T) {
	work := initRepoWithBranch(t)
	// Commit a subscription file on a non-checked-out date branch.
	repoGit(t, work, "switch", "-c", "trending/daily/2026-10-30")
	if err := os.MkdirAll(filepath.Join(work, "digests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "digests", "subscriptions.yaml"),
		[]byte("schema: 1\nsubscriptions:\n  - a/b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repoGit(t, work, "add", "-A")
	repoGit(t, work, "commit", "-m", "subs")
	repoGit(t, work, "switch", "main") // leave main checked out

	branch := RunBranchName("", "daily", "2026-10-30")
	st, err := ReadRepoOnTargetBranch(work, "", "subscriptions.yaml", branch)
	if err != nil {
		t.Fatalf("ReadRepoOnTargetBranch: %v", err)
	}
	if got := st.List(); len(got) != 1 || got[0] != "a/b" {
		t.Fatalf("branch file list = %v, want [a/b]", got)
	}

	// Pending adds c/d; merged must be a/b,c/d.
	if err := WritePending(work, []string{"c/d"}); err != nil {
		t.Fatal(err)
	}
	pend, ok, err := ReadPending(work)
	if err != nil || !ok {
		t.Fatalf("ReadPending ok=%v err=%v", ok, err)
	}
	merged, added := MergeUnique(st.List(), pend)
	if added != 1 || strings.Join(merged, ",") != "a/b,c/d" {
		t.Fatalf("merged=%v added=%d, want [a/b c/d] 1", merged, added)
	}
}

// Missing branch => empty store, no error.
func TestReadRepoOnMissingBranchIsEmpty(t *testing.T) {
	work := initRepoWithBranch(t)
	st, err := ReadRepoOnTargetBranch(work, "", "subscriptions.yaml", "trending/daily/2099-01-01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.Len() != 0 {
		t.Fatalf("list = %v, want empty", st.List())
	}
}

// Malformed YAML on the branch must fail fast, not silently fall back to empty.
func TestReadRepoOnTargetBranchMalformedFails(t *testing.T) {
	work := initRepoWithBranch(t)
	repoGit(t, work, "switch", "-c", "trending/daily/2026-10-30")
	if err := os.MkdirAll(filepath.Join(work, "digests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "digests", "subscriptions.yaml"),
		[]byte("subscriptions: [ not, valid : :\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repoGit(t, work, "add", "-A")
	repoGit(t, work, "commit", "-m", "bad")
	if _, err := ReadRepoOnTargetBranch(work, "", "subscriptions.yaml", "trending/daily/2026-10-30"); err == nil {
		t.Fatal("expected parse error for malformed branch file")
	}
}
