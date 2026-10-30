// Package serve exposes rendered digests over HTTP and embeds a placeholder UI.
package serve

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/ipc"
)

// ErrNotFound is returned when a digest for the requested date is absent.
var ErrNotFound = errors.New("digest not found")

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// Store reads digests from a directory laid out as <root>/<date>/<date>.json,
// where root is the digests directory (usually "digests").
type Store struct {
	root string
}

// NewStore returns a Store rooted at the digests directory.
func NewStore(root string) *Store {
	if root == "" {
		root = "digests"
	}
	return &Store{root: root}
}

func (s *Store) path(date string) string {
	return filepath.Join(s.root, date, date+".json")
}

// Digest loads one day's digest.
func (s *Store) Digest(date string) (*digest.Digest, error) {
	if !dateRe.MatchString(date) {
		return nil, ErrNotFound
	}
	data, err := os.ReadFile(s.path(date))
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var d digest.Digest
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// ManifestEntry is one row of the manifest.
type ManifestEntry struct {
	Date        string `json:"date"`
	ContentHash string `json:"contentHash"`
	Degraded    bool   `json:"degraded"`
}

// Manifest lists available digests, newest first.
type Manifest struct {
	CoreVersion string          `json:"coreVersion"`
	Entries     []ManifestEntry `json:"entries"`
}

// Manifest scans the digests directory.
func (s *Store) Manifest() (Manifest, error) {
	entries, err := os.ReadDir(s.root)
	if os.IsNotExist(err) {
		return Manifest{CoreVersion: ipc.Version, Entries: []ManifestEntry{}}, nil
	}
	if err != nil {
		return Manifest{}, err
	}
	out := make([]ManifestEntry, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || !dateRe.MatchString(e.Name()) {
			continue
		}
		d, err := s.Digest(e.Name())
		if err != nil {
			continue
		}
		out = append(out, ManifestEntry{Date: d.Date, ContentHash: d.ContentHash, Degraded: d.Degraded})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date > out[j].Date })
	return Manifest{CoreVersion: ipc.Version, Entries: out}, nil
}
