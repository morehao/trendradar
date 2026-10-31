// Package subscription manages the user's manual repo subscription list. The
// list lives in its own YAML file so the CLI can add/remove entries without
// rewriting config.yaml (which would drop comments) and so run and subscribe
// share one resolution rule.
package subscription

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultFile is used when neither --file nor releases.subscriptionsFile is set.
const DefaultFile = "subscriptions.yaml"

const (
	fileSchema = 1
	maxRepos   = 500
)

// ErrNotSubscribed is returned by Remove when the repo is not in the list.
var ErrNotSubscribed = errors.New("subscription: repo not subscribed")

// reFullName accepts the GitHub owner/repo shape used throughout the project.
var reFullName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// file is the on-disk shape. The key is "subscriptions" (not the retired
// config.repos) so the two concepts cannot be confused.
type file struct {
	Schema        int      `yaml:"schema"`
	Subscriptions []string `yaml:"subscriptions"`
}

// Store is a loaded subscription list. Path is always absolute.
type Store struct {
	Path  string
	repos []string
	seen  map[string]struct{}
}

// ResolveSubscriptionsPath is the single resolution rule shared by `run` and
// `subscribe`:
//
//	configured non-empty -> configured
//	otherwise           -> ./subscriptions.yaml
//
// A relative path is resolved against the directory of configPath (so a cron
// job whose CWD differs still finds the same file); with no configPath it is
// resolved against the current working directory.
func ResolveSubscriptionsPath(configPath, configured string) (string, error) {
	p := strings.TrimSpace(configured)
	if p == "" {
		p = DefaultFile
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	base := "."
	if strings.TrimSpace(configPath) != "" {
		ap, err := filepath.Abs(configPath)
		if err != nil {
			return "", fmt.Errorf("subscription: resolve config path: %w", err)
		}
		base = filepath.Dir(ap)
	}
	abs, err := filepath.Abs(filepath.Join(base, p))
	if err != nil {
		return "", fmt.Errorf("subscription: resolve subscriptions path: %w", err)
	}
	return abs, nil
}

// Load reads a subscription file. A missing file is not an error (empty list);
// malformed YAML is. Invalid entries fail fast so a typo does not silently
// subscribe to nothing.
func Load(path string) (*Store, error) {
	s := &Store{Path: path, seen: make(map[string]struct{})}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("subscription: read %s: %w", path, err)
	}
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

// List returns the subscriptions in insertion order.
func (s *Store) List() []string {
	out := make([]string, len(s.repos))
	copy(out, s.repos)
	return out
}

// Len reports how many subscriptions exist.
func (s *Store) Len() int { return len(s.repos) }

// Add validates then appends repos, skipping duplicates. The whole batch is
// validated first so an invalid entry never leaves a partial write.
func (s *Store) Add(repos ...string) (int, error) {
	clean := make([]string, 0, len(repos))
	for _, raw := range repos {
		r := strings.TrimSpace(raw)
		if !reFullName.MatchString(r) {
			return 0, fmt.Errorf("subscription: invalid repo %q (want owner/repo)", raw)
		}
		clean = append(clean, r)
	}
	// Pre-check capacity before mutating state.
	toAdd := 0
	for _, r := range clean {
		if _, ok := s.seen[r]; !ok {
			toAdd++
		}
	}
	if len(s.repos)+toAdd > maxRepos {
		return 0, fmt.Errorf("subscription: too many repos (%d > %d)", len(s.repos)+toAdd, maxRepos)
	}
	added := 0
	for _, r := range clean {
		if _, ok := s.seen[r]; ok {
			continue
		}
		s.seen[r] = struct{}{}
		s.repos = append(s.repos, r)
		added++
	}
	if added > 0 {
		if err := s.Save(); err != nil {
			return 0, err
		}
	}
	return added, nil
}

// Remove deletes one repo and saves. Removing a missing repo returns
// ErrNotSubscribed so the CLI can exit non-zero.
func (s *Store) Remove(repo string) error {
	r := strings.TrimSpace(repo)
	if _, ok := s.seen[r]; !ok {
		return ErrNotSubscribed
	}
	delete(s.seen, r)
	out := s.repos[:0]
	for _, existing := range s.repos {
		if existing != r {
			out = append(out, existing)
		}
	}
	s.repos = out
	return s.Save()
}

// Save writes the file atomically (temp + rename) with 0600 permissions.
func (s *Store) Save() error {
	if s.Path == "" {
		return errors.New("subscription: empty path")
	}
	f := file{Schema: fileSchema, Subscriptions: s.List()}
	data, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("subscription: marshal: %w", err)
	}
	dir := filepath.Dir(s.Path)
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
	if err := os.Rename(name, s.Path); err != nil {
		return fmt.Errorf("subscription: rename: %w", err)
	}
	return nil
}
