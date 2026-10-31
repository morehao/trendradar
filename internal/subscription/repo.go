package subscription

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/render"

	"gopkg.in/yaml.v3"
)

// pendingFile is the machine-local intent queue. It lives under .git/ so that
// `git status` never sees it: `run`'s IsClean check (gitx.IsClean) treats any
// modification of a *tracked* file as dirty and aborts with REPO_DIRTY, so a
// subscription add must not touch the working tree. The file is replayed into
// the versioned subscription file by the next run and deleted only after that
// run's commit AND push both succeed.
const pendingFile = "trendradar-subscriptions.pending"

// errGitShowNotFound marks "the branch or path does not exist yet", which is not
// an error: a date with no run has no subscription file on its branch.
var errGitShowNotFound = errors.New("subscription: not found on branch")

// RepoSubscriptionsPath resolves the versioned subscription file inside the data
// repo: <repoPath>/<OutputRoot(outputDir)>/<file>, as an absolute path.
//
// outputDir is normalised here through render.OutputRoot — the single source of
// truth. Callers (run / serve / subscribe) must pass the *raw* cfg.Output.Dir,
// not a pre-normalised value: the default cfg.Output.Dir is "" and
// render.OutputRoot("")=="digests", so normalising in two different places
// (say run as "digests" and serve as "") would make serve read
// <repo>/subscriptions.yaml while run reads <repo>/digests/subscriptions.yaml,
// breaking G1 (three-way same-source). Empirically this mismatch silently split
// the two.
//
// It also rejects a file that would escape the normalised outDir (absolute path
// or ".."), because g.AddAll(outDir) would then not stage it and the
// subscription change would be silently dropped from the commit (F7).
func RepoSubscriptionsPath(repoPath, outputDir, file string) (string, error) {
	if strings.TrimSpace(repoPath) == "" {
		return "", errors.New("subscription: repo path is empty")
	}
	root := render.OutputRoot(outputDir)
	f := strings.TrimSpace(file)
	if f == "" {
		f = DefaultFile
	}
	if filepath.IsAbs(f) {
		return "", fmt.Errorf("subscription: subscriptions file %q must be relative to output dir %q", file, root)
	}
	// Normalise the slash form first so a Windows-style "..\\x" cannot slip past.
	clean := filepath.Clean(filepath.FromSlash(filepath.ToSlash(f)))
	joined := filepath.ToSlash(filepath.Join(root, filepath.ToSlash(clean)))
	inRoot := joined == root || strings.HasPrefix(joined+"/", root+"/")
	if !inRoot {
		return "", fmt.Errorf("subscription: subscriptions file %q escapes output dir %q", file, root)
	}
	return filepath.Join(repoPath, filepath.FromSlash(joined)), nil
}

// PendingPath returns <repoPath>/.git/trendradar-subscriptions.pending.
func PendingPath(repoPath string) string {
	return filepath.Join(repoPath, ".git", pendingFile)
}

// RunBranchName derives the per-date target branch exactly as app.Run does:
//
//	<BranchPrefix|"trending/"> + <period> + "/" + render.NameOf(period, date)
//
// This is the single source of truth for the branch name; subscribe and serve
// must not re-implement it, or a subscribe on one day would read a different
// branch than run writes. Mirrors internal/app/run.go (prefix default + branch
// construction) and internal/render/period.go NameOf.
func RunBranchName(branchPrefix, period, date string) string {
	prefix := branchPrefix
	if prefix == "" {
		prefix = "trending/"
	}
	p := render.PeriodOf(digest.Digest{Period: period})
	return prefix + p + "/" + render.NameOf(p, date)
}

// ReadRepoOnTargetBranch reads the subscription file on the given branch of the
// data repo, without checking it out. It reads via `git show <branch>:<path>` so
// the caller does not have to be on that branch — subscribe runs outside a run,
// often while the repo is on main, and reading the checked-out branch would pick
// up a stale baseline from a previous date's branch.
//
// A missing branch (the date has not had its first run yet) or a missing file
// yields an empty Store, not an error. Malformed YAML still fails fast.
func ReadRepoOnTargetBranch(repoPath, outputDir, file, branch string) (*Store, error) {
	abs, err := RepoSubscriptionsPath(repoPath, outputDir, file)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(repoPath, abs)
	if err != nil {
		return nil, fmt.Errorf("subscription: relativize %s: %w", abs, err)
	}
	data, err := gitShow(repoPath, branch, filepath.ToSlash(rel))
	if err != nil {
		if errors.Is(err, errGitShowNotFound) {
			return emptyStore(abs), nil
		}
		return nil, err
	}
	return loadBytes(abs, data)
}

// WritePending atomically writes repos to the pending queue (never the working
// tree). Empty repos removes any existing pending file so a run has nothing to
// replay.
func WritePending(repoPath string, repos []string) error {
	pp := PendingPath(repoPath)
	if len(repos) == 0 {
		if err := os.Remove(pp); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("subscription: remove pending: %w", err)
		}
		return nil
	}
	data, err := marshalList(repos)
	if err != nil {
		return err
	}
	return writeFileAtomic(pp, data)
}

// ReadPending returns the pending list. The bool is false when no pending file
// exists (hasPending==false); a present-but-empty file returns ([], true).
func ReadPending(repoPath string) ([]string, bool, error) {
	pp := PendingPath(repoPath)
	data, err := os.ReadFile(pp)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("subscription: read pending: %w", err)
	}
	st, err := loadBytes(pp, data)
	if err != nil {
		return nil, false, err
	}
	return st.List(), true, nil
}

// ClearPending deletes the pending queue. Callers MUST invoke it only after both
// the commit and the push succeeded. Clearing earlier would lose the
// subscription change if the push then fails; keeping it means the next run may
// replay the commit once — repeating a commit is preferable to losing changes.
func ClearPending(repoPath string) error {
	pp := PendingPath(repoPath)
	if err := os.Remove(pp); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("subscription: clear pending: %w", err)
	}
	return nil
}

// ValidateList checks that every entry matches owner/repo and that the list
// stays within maxRepos, without touching disk. subscribe uses it to reject a
// bad entry before it reaches the pending queue.
func ValidateList(repos []string) ([]string, error) {
	out := make([]string, 0, len(repos))
	for _, raw := range repos {
		r := strings.TrimSpace(raw)
		if !reFullName.MatchString(r) {
			return nil, fmt.Errorf("subscription: invalid repo %q (want owner/repo)", raw)
		}
		out = append(out, r)
	}
	if len(out) > maxRepos {
		return nil, fmt.Errorf("subscription: too many repos (%d > %d)", len(out), maxRepos)
	}
	return out, nil
}

// SaveList writes repos to an explicit filesystem path in the canonical YAML
// shape (schema:1 + subscriptions). It is what `run` uses to land the merged
// subscription set on the checked-out date branch. The write is atomic; the
// caller is responsible for having validated the entries.
func SaveList(path string, repos []string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("subscription: empty path")
	}
	data, err := marshalList(repos)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

// MergeUnique returns a ∪ b, preserving a's order then appending b's unseen
// entries, plus the count of newly added entries from b.
func MergeUnique(a, b []string) (merged []string, added int) {
	seen := make(map[string]struct{}, len(a)+len(b))
	merged = make([]string, 0, len(a)+len(b))
	for _, r := range a {
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		merged = append(merged, r)
	}
	for _, r := range b {
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		merged = append(merged, r)
		added++
	}
	return merged, added
}

// gitShow returns the bytes of relPath on branch via `git show branch:path`. A
// missing branch or path is reported as errGitShowNotFound so callers can treat
// "the date has not had its first run" as an empty list rather than a failure.
func gitShow(repoPath, branch, relPath string) ([]byte, error) {
	if strings.TrimSpace(branch) == "" {
		return nil, errGitShowNotFound
	}
	cmd := exec.Command("git", "show", branch+":"+relPath)
	cmd.Dir = repoPath
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		// `git show badref:path` exits non-zero; treat any failure as "not found
		// on branch". A malformed ref would also land here and legitimately mean
		// there is no baseline yet, which is the empty-list case.
		return nil, errGitShowNotFound
	}
	return out.Bytes(), nil
}

// emptyStore returns a Store bound to path with no repos.
func emptyStore(path string) *Store {
	return &Store{Path: path, seen: make(map[string]struct{})}
}

// loadBytes parses subscription YAML from data, validating entries exactly as
// Load does (fail fast on a bad entry so a typo never silently unsubscribes).
func loadBytes(path string, data []byte) (*Store, error) {
	s := emptyStore(path)
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("subscription: parse %s: %w", path, err)
	}
	for _, raw := range f.Subscriptions {
		r := strings.TrimSpace(raw)
		if r == "" {
			continue
		}
		if !reFullName.MatchString(r) {
			return nil, fmt.Errorf("subscription: invalid repo %q in %s", r, path)
		}
		if _, ok := s.seen[r]; ok {
			continue
		}
		s.seen[r] = struct{}{}
		s.repos = append(s.repos, r)
	}
	if len(s.repos) > maxRepos {
		return nil, fmt.Errorf("subscription: too many repos (%d > %d)", len(s.repos), maxRepos)
	}
	return s, nil
}

// marshalList serialises repos to the same YAML shape Save writes.
func marshalList(repos []string) ([]byte, error) {
	data, err := yaml.Marshal(file{Schema: fileSchema, Subscriptions: repos})
	if err != nil {
		return nil, fmt.Errorf("subscription: marshal: %w", err)
	}
	return data, nil
}

// writeFileAtomic writes data to path via a temp file in the same directory plus
// rename, with 0600 permissions. It creates parent directories as needed.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("subscription: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".subscriptions-*")
	if err != nil {
		return fmt.Errorf("subscription: temp file: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("subscription: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("subscription: rename: %w", err)
	}
	return nil
}
