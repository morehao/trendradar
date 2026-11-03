// Package observe is the local star observation store: it records one star
// snapshot per repository per (UTC) day and computes real star deltas from those
// snapshots, so "stars today" no longer depends on the trending page.
//
// Storage is append-only JSONL, one shard per month
// (stars-YYYY-MM.jsonl). Writes append a single line; deduplication by
// (repoId, date) happens when the shard is loaded, keeping the newest line for a
// given key (last write wins). Compaction rewrites a shard atomically (temp file
// + rename) keeping only the deduplicated rows, and skips — counting, never
// aborting on — corrupt lines. Every stored date is UTC (plan D1); rendering
// converts to the configured timezone.
package observe

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SchemaVersion is written into the manifest so an older/newer tool can refuse
// to interpret a store it does not understand.
const SchemaVersion = 1

// shardPrefix / shardSuffix delimit the per-month snapshot shards.
const (
	shardPrefix  = "stars-"
	shardSuffix  = ".jsonl"
	manifestName = "manifest.json"
)

// Snapshot is one observed star state for a repository on a UTC date. It is the
// unit stored in the shards and the input to delta computation.
type Snapshot struct {
	RepoID string `json:"repoId"`
	Date   string `json:"date"` // UTC date, "YYYY-MM-DD"
	Stars  int    `json:"stars"`
	Forks  int    `json:"forks"`
	Source string `json:"source"` // "trending-html" | "search-api" | "snapshot" | "backfill"
}

// key returns the deduplication key for a snapshot.
func (s Snapshot) key() string { return s.RepoID + "\x00" + s.Date }

// validate rejects snapshots that cannot be stored (missing id/date or a
// malformed date) so a bad row cannot poison the shard.
func (s Snapshot) validate() error {
	if strings.TrimSpace(s.RepoID) == "" {
		return errors.New("observe: empty repoId")
	}
	if _, err := time.Parse(dateLayout, s.Date); err != nil {
		return fmt.Errorf("observe: invalid date %q: %w", s.Date, err)
	}
	return nil
}

const dateLayout = "2006-01-02"

// Store is an append-only, month-sharded JSONL observation store rooted at Dir.
// It is safe for concurrent use within a single process.
type Store struct {
	dir string

	mu      sync.Mutex
	loaded  map[string]bool                // shard month ("YYYY-MM") -> loaded into data
	data    map[string]map[string]Snapshot // month -> key -> snapshot
	skipped int                            // corrupt lines skipped across all loads
}

// Open opens (creating if needed) a store rooted at dir. Loading is lazy: shards
// are read on first access to their month, and the manifest is read for counts.
func Open(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("observe: empty store dir")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("observe: create store dir: %w", err)
	}
	return &Store{
		dir:    dir,
		loaded: make(map[string]bool),
		data:   make(map[string]map[string]Snapshot),
	}, nil
}

// Dir returns the store root directory.
func (s *Store) Dir() string { return s.dir }

// DefaultStoreDir resolves the default observation store directory under
// $DSH_HOME (plan D4: the store lives in DSH's home, never in the user's digest
// repo). Resolution order for the home: $DSH_HOME, then $HOME/.dsh. The trailing
// path is github-trending/observation, matching the plugin's home layout.
func DefaultStoreDir() string {
	home := os.Getenv("DSH_HOME")
	if strings.TrimSpace(home) == "" {
		if userHome, err := os.UserHomeDir(); err == nil && userHome != "" {
			home = filepath.Join(userHome, ".dsh")
		}
	}
	if strings.TrimSpace(home) == "" {
		// Last resort: a relative dir, so a run still works in a bare environment.
		return filepath.Join("github-trending", "observation")
	}
	return filepath.Join(home, "github-trending", "observation")
}

// Skipped returns how many corrupt lines were skipped during loads so far. A
// non-zero value is a signal (surfaced by callers as a warning), never a fatal
// error: one bad line must not take the whole observation history down.
func (s *Store) Skipped() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skipped
}

// shardPath returns the JSONL path for the month of date.
func (s *Store) shardPath(date string) string {
	return filepath.Join(s.dir, shardPrefix+monthOf(date)+shardSuffix)
}

func monthOf(date string) string {
	if len(date) >= 7 {
		return date[:7]
	}
	return date
}

// Put records one observation. Deduplication by (repoId, date) is by month: the
// snapshot is appended and the in-memory view keeps the newest value, so a
// same-day rewrite overwrites the prior value on load/compact. It returns the
// previous snapshot (if any) so callers can tell whether this was an overwrite.
func (s *Store) Put(snap Snapshot) (Snapshot, bool, error) {
	if err := snap.validate(); err != nil {
		return Snapshot{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	month := monthOf(snap.Date)
	if err := s.ensureLoaded(month); err != nil {
		return Snapshot{}, false, err
	}
	prev, existed := s.data[month][snap.key()]

	line, err := json.Marshal(snap)
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("observe: marshal snapshot: %w", err)
	}
	f, err := os.OpenFile(s.shardPath(snap.Date), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("observe: open shard: %w", err)
	}
	// One write of a bounded, well-formed line; a trailing newline delimits it.
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return Snapshot{}, false, fmt.Errorf("observe: append shard: %w", err)
	}
	if err := f.Close(); err != nil {
		return Snapshot{}, false, fmt.Errorf("observe: close shard: %w", err)
	}

	if s.data[month] == nil {
		s.data[month] = make(map[string]Snapshot)
	}
	s.data[month][snap.key()] = snap
	return prev, existed, nil
}

// Snapshots returns every stored snapshot for a repo, sorted by date ascending.
func (s *Store) Snapshots(repoID string) ([]Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	months, err := s.listMonths()
	if err != nil {
		return nil, err
	}
	var out []Snapshot
	for _, m := range months {
		if err := s.ensureLoaded(m); err != nil {
			return nil, err
		}
		for _, snap := range s.data[m] {
			if snap.RepoID == repoID {
				out = append(out, snap)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out, nil
}

// Lookup returns the snapshot for (repoID, date) and whether it exists.
func (s *Store) Lookup(repoID, date string) (Snapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	month := monthOf(date)
	if err := s.ensureLoaded(month); err != nil {
		return Snapshot{}, false, err
	}
	snap, ok := s.data[month][repoID+"\x00"+date]
	return snap, ok, nil
}

// listMonths returns the months that have an on-disk shard, sorted ascending.
func (s *Store) listMonths() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("observe: read store dir: %w", err)
	}
	var months []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, shardPrefix) || !strings.HasSuffix(name, shardSuffix) {
			continue
		}
		m := strings.TrimSuffix(strings.TrimPrefix(name, shardPrefix), shardSuffix)
		months = append(months, m)
	}
	sort.Strings(months)
	return months, nil
}

// ensureLoaded loads the shard for month into memory once. Must be called with
// s.mu held.
func (s *Store) ensureLoaded(month string) error {
	if s.loaded[month] {
		return nil
	}
	if s.data[month] == nil {
		s.data[month] = make(map[string]Snapshot)
	}
	path := filepath.Join(s.dir, shardPrefix+month+shardSuffix)
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.loaded[month] = true
			return nil
		}
		return fmt.Errorf("observe: open shard %s: %w", month, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		raw := sc.Bytes()
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		var snap Snapshot
		if err := json.Unmarshal(raw, &snap); err != nil {
			s.skipped++
			continue
		}
		if err := snap.validate(); err != nil {
			s.skipped++
			continue
		}
		// Last write wins for a given (repoId, date).
		s.data[month][snap.key()] = snap
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("observe: scan shard %s: %w", month, err)
	}
	s.loaded[month] = true
	return nil
}

// Compact rewrites every loaded (and on-disk) shard atomically, keeping only the
// deduplicated rows sorted by (repoId, date), and refreshes the manifest. Corrupt
// lines are dropped (already counted in Skipped). Compaction is safe to call
// repeatedly and is the way to physically reclaim duplicate appended lines.
func (s *Store) Compact() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	months, err := s.listMonths()
	if err != nil {
		return err
	}
	total := 0
	repos := make(map[string]struct{})
	for _, m := range months {
		if err := s.ensureLoaded(m); err != nil {
			return err
		}
		if err := s.rewriteShard(m); err != nil {
			return err
		}
		for _, snap := range s.data[m] {
			total++
			repos[snap.RepoID] = struct{}{}
		}
	}
	return s.writeManifest(manifest{
		Schema:          SchemaVersion,
		LastCompactedAt: time.Now().UTC().Format(time.RFC3339),
		SnapshotCount:   total,
		RepoCount:       len(repos),
	})
}

// rewriteShard atomically rewrites one shard from the in-memory dedup view.
func (s *Store) rewriteShard(month string) error {
	path := filepath.Join(s.dir, shardPrefix+month+shardSuffix)
	rows := make([]Snapshot, 0, len(s.data[month]))
	for _, snap := range s.data[month] {
		rows = append(rows, snap)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].RepoID != rows[j].RepoID {
			return rows[i].RepoID < rows[j].RepoID
		}
		return rows[i].Date < rows[j].Date
	})

	tmp, err := os.CreateTemp(s.dir, shardPrefix+month+"-*.tmp")
	if err != nil {
		return fmt.Errorf("observe: temp shard %s: %w", month, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename
	bw := bufio.NewWriter(tmp)
	for _, snap := range rows {
		line, err := json.Marshal(snap)
		if err != nil {
			_ = tmp.Close()
			return fmt.Errorf("observe: marshal during compact: %w", err)
		}
		if _, err := bw.Write(append(line, '\n')); err != nil {
			_ = tmp.Close()
			return fmt.Errorf("observe: write during compact: %w", err)
		}
	}
	if err := bw.Flush(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("observe: flush during compact: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("observe: sync during compact: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("observe: close during compact: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("observe: rename compacted shard: %w", err)
	}
	return nil
}

// manifest is the small metadata file at the store root.
type manifest struct {
	Schema          int    `json:"schema"`
	LastCompactedAt string `json:"lastCompactedAt"`
	SnapshotCount   int    `json:"snapshotCount"`
	RepoCount       int    `json:"repoCount"`
}

// writeManifest writes the manifest atomically (temp + rename). Must hold s.mu.
func (s *Store) writeManifest(m manifest) error {
	buf, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("observe: marshal manifest: %w", err)
	}
	tmp, err := os.CreateTemp(s.dir, manifestName+".*.tmp")
	if err != nil {
		return fmt.Errorf("observe: temp manifest: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(append(buf, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("observe: write manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("observe: close manifest: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(s.dir, manifestName)); err != nil {
		return fmt.Errorf("observe: rename manifest: %w", err)
	}
	return nil
}
