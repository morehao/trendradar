package releasewatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// stateSchema is the on-disk cursor schema version.
const stateSchema = 1

// maxSeenTags caps the per-repo tag memory so the state file cannot grow forever.
const maxSeenTags = 200

// RepoState is the per-repository cursor: the newest processed release plus a
// bounded memory of tags (used when a repo has no releases and we fall back to
// tags).
type RepoState struct {
	LastID          int64      `json:"lastId"`
	LastTag         string     `json:"lastTag,omitempty"`
	LastPublishedAt *time.Time `json:"lastPublishedAt,omitempty"`
	SeenTags        []string   `json:"seenTags,omitempty"`
}

// State is the persisted cursor for every subscribed repo. It is machine-local
// and lives under <repo>/.git so it is never committed.
type State struct {
	Schema    int                  `json:"schema"`
	UpdatedAt *time.Time           `json:"updatedAt,omitempty"`
	Repos     map[string]RepoState `json:"repos"`
}

// StatePath returns <repo>/.git/trendradar-release-state.json.
func StatePath(repoPath string) string {
	return filepath.Join(repoPath, ".git", "trendradar-release-state.json")
}

// NewState returns an empty state.
func NewState() *State { return &State{Schema: stateSchema, Repos: map[string]RepoState{}} }

// LoadState reads a cursor file. A missing file yields an empty state; a corrupt
// file is an error because the cursor is a source of truth for deduplication.
func LoadState(path string) (*State, error) {
	s := NewState()
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	if s.Repos == nil {
		s.Repos = map[string]RepoState{}
	}
	s.Schema = stateSchema
	return s, nil
}

// Get returns the cursor for a repo.
func (s *State) Get(repo string) (RepoState, bool) {
	st, ok := s.Repos[repo]
	return st, ok
}

// Set stores a cursor and stamps UpdatedAt.
func (s *State) Set(repo string, st RepoState) {
	if s.Repos == nil {
		s.Repos = map[string]RepoState{}
	}
	if len(st.SeenTags) > maxSeenTags {
		st.SeenTags = st.SeenTags[len(st.SeenTags)-maxSeenTags:]
	}
	s.Repos[repo] = st
	now := time.Now().UTC()
	s.UpdatedAt = &now
}

// AddSeenTag appends a tag to the bounded memory, deduping.
func AddSeenTag(st *RepoState, tag string) {
	if tag == "" {
		return
	}
	for _, t := range st.SeenTags {
		if t == tag {
			return
		}
	}
	st.SeenTags = append(st.SeenTags, tag)
	if len(st.SeenTags) > maxSeenTags {
		st.SeenTags = st.SeenTags[len(st.SeenTags)-maxSeenTags:]
	}
}

// Save writes the cursor atomically with 0600 permissions.
func (s *State) Save(path string) error {
	if path == "" {
		return nil
	}
	if s.Repos == nil {
		s.Repos = map[string]RepoState{}
	}
	s.Schema = stateSchema
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".release-state-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
