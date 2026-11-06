package releasewatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// stateSchema is the on-disk cursor schema version. Version 3 adds SummarizedTags
// (tags whose summary was actually generated and published). Older files still
// load because every new field is additive; a v2 file simply has no summarized
// memory and will re-summarize anything not covered by the release ID cursor.
const stateSchema = 3

// maxSeenTags caps the per-repo tag memory so the state file cannot grow forever.
const maxSeenTags = 200

// maxSummarizedTags caps the summarized-tag memory. It is the incremental cursor:
// a tag here means "already summarized and published", so it is never redone
// unless a forced regeneration explicitly asks for it.
const maxSummarizedTags = 200

// Per-repo status values. They mirror contract SubscriptionStatus exactly; the
// generated type cannot be used here because the state file is machine-local and
// must stay loadable even if a future contract drops a value.
const (
	StatusBaseline    = "baseline"
	StatusOK          = "ok"
	StatusNoChange    = "no_change"
	StatusRateLimited = "rate_limited"
	StatusFailed      = "failed"
)

// RepoState is the per-repository cursor: the newest processed release plus the
// bounded tag memory used when a repo has no releases and we fall back to tags.
//
// SeenTags is "tags we have ever observed" (including a baseline page) and is
// used to tell "brand new" from "seen before" — it is NOT the incremental cursor.
// SummarizedTags is the incremental cursor proper: a tag is added only after its
// summary was generated and the document published. Incremental work is
// "current tags − SummarizedTags", so a baseline page that pre-records seen tags
// no longer swallows the versions in between.
//
// WeekKey is the ISO week whose *scheduled* check completed successfully
// (including "nothing new"). It is the weekly gate for schedule runs only: a
// second *scheduled* run in the same week skips the repo without touching GitHub.
// Manual / forced runs ignore it (they still respect SummarizedTags), so an
// explicit re-run can always pick up versions the week gate would otherwise hide.
// A failed check does NOT set it.
type RepoState struct {
	LastID          int64      `json:"lastId"`
	LastTag         string     `json:"lastTag,omitempty"`
	LastPublishedAt *time.Time `json:"lastPublishedAt,omitempty"`
	SeenTags        []string   `json:"seenTags,omitempty"`
	SummarizedTags  []string   `json:"summarizedTags,omitempty"`

	WeekKey         string     `json:"weekKey,omitempty"`
	Status          string     `json:"status,omitempty"`
	LatestSummaryAt string     `json:"latestSummaryAt,omitempty"`
	LastCheckedAt   *time.Time `json:"lastCheckedAt,omitempty"`
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
	if len(st.SummarizedTags) > maxSummarizedTags {
		st.SummarizedTags = st.SummarizedTags[len(st.SummarizedTags)-maxSummarizedTags:]
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

// AddSummarizedTag records that a tag's summary was generated and published, so
// it becomes part of the incremental cursor and is not redone by later runs.
func AddSummarizedTag(st *RepoState, tag string) {
	if tag == "" {
		return
	}
	for _, t := range st.SummarizedTags {
		if t == tag {
			return
		}
	}
	st.SummarizedTags = append(st.SummarizedTags, tag)
	if len(st.SummarizedTags) > maxSummarizedTags {
		st.SummarizedTags = st.SummarizedTags[len(st.SummarizedTags)-maxSummarizedTags:]
	}
}

// hasSummarized reports whether a tag is already in the incremental cursor.
func hasSummarized(st RepoState, tag string) bool {
	for _, t := range st.SummarizedTags {
		if t == tag {
			return true
		}
	}
	return false
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
