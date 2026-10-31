// Package serve exposes rendered digests over HTTP and embeds a placeholder UI.
package serve

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	contract "github.com/morehao/trendradar/contract"
	"github.com/morehao/trendradar/internal/ipc"
	"github.com/morehao/trendradar/internal/render"
)

// ErrNotFound is returned when a digest for the requested date is absent.
var ErrNotFound = errors.New("digest not found")

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// Store reads digests from a directory laid out as
// <root>/<period>/<bucket>/<name>.json, where (bucket, name) are derived from
// (period, date) by the shared render helpers so serve and write never diverge.
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

// path resolves the on-disk file for (period, date). date is always the period
// end date (YYYY-MM-DD); the bucket and filename are period-specific.
func (s *Store) path(period, date string) string {
	return filepath.Join(s.root, period, render.BucketOf(period, date), render.NameOf(period, date)+".json")
}

// Digest loads one period's digest normalized to the v2 contract shape.
// Historical v1 files are upgraded on read (schema:2, contentHashAlgo legacy,
// hash kept).
func (s *Store) Digest(period, date string) (*contract.Digest, error) {
	if !dateRe.MatchString(date) {
		return nil, ErrNotFound
	}
	return readDigestFile(s.path(period, date))
}

// readDigestFile reads a digest JSON file, normalizing v1 files to the v2 shape.
func readDigestFile(p string) (*contract.Digest, error) {
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var probe map[string]json.RawMessage
	_ = json.Unmarshal(data, &probe)
	if _, ok := probe["contentHashAlgo"]; ok {
		var d contract.Digest
		if err := json.Unmarshal(data, &d); err != nil {
			return nil, err
		}
		return &d, nil
	}
	// v1 file: normalize to v2 with the legacy algorithm, preserving the hash.
	var cd contract.Digest
	if err := json.Unmarshal(data, &cd); err != nil {
		return nil, err
	}
	cd.Schema = 2
	cd.ContentHashAlgo = "legacy-struct-marshal-v1"
	return &cd, nil
}

// ManifestEntry is one row of the manifest.
type ManifestEntry struct {
	Date            string `json:"date"`
	Period          string `json:"period"`
	ContentHash     string `json:"contentHash"`
	Schema          int    `json:"schema"`
	ContentHashAlgo string `json:"contentHashAlgo"`
	Degraded        bool   `json:"degraded"`
}

// Manifest lists available digests, newest first.
type Manifest struct {
	CoreVersion string          `json:"coreVersion"`
	Entries     []ManifestEntry `json:"entries"`
}

// Manifest scans the digests directory. An empty period scans all periods; a
// non-empty one restricts the scan to that period.
func (s *Store) Manifest(period string) (Manifest, error) {
	var periods []string
	if period == "" {
		periods = []string{render.PeriodDaily, render.PeriodWeekly, render.PeriodMonthly}
	} else {
		periods = []string{period}
	}

	out := []ManifestEntry{}
	for _, p := range periods {
		entries, err := s.scanPeriod(p)
		if err != nil {
			return Manifest{}, err
		}
		out = append(out, entries...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date > out[j].Date
		}
		return out[i].Period > out[j].Period
	})
	return Manifest{CoreVersion: ipc.Version, Entries: out}, nil
}

// scanPeriod walks root/<period>/<bucket>/ and reads each matching digest file.
func (s *Store) scanPeriod(period string) ([]ManifestEntry, error) {
	periodDir := filepath.Join(s.root, period)
	buckets, err := os.ReadDir(periodDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []ManifestEntry
	for _, b := range buckets {
		if !b.IsDir() {
			continue
		}
		dir := filepath.Join(periodDir, b.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			d, err := readDigestFile(filepath.Join(dir, f.Name()))
			if err != nil {
				continue
			}
			out = append(out, ManifestEntry{
				Date:            d.Date,
				Period:          period,
				ContentHash:     d.ContentHash,
				Schema:          d.Schema,
				ContentHashAlgo: d.ContentHashAlgo,
				Degraded:        d.Degraded,
			})
		}
	}
	return out, nil
}
