package serve

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"

	contract "github.com/morehao/trendradar/contract"
	"github.com/morehao/trendradar/internal/render"
	"github.com/morehao/trendradar/internal/subscription"
)

// ErrReleasesNotFound is returned when a release digest for the requested date
// is absent.
var ErrReleasesNotFound = errors.New("releases not found")

// releaseRoot resolves the on-disk release directory. sub is the configured
// releases sub-directory (config releases.outputDir, default "releases") and is
// normalized by render.ReleaseRoot so serve and the writer never diverge.
func (s *Store) releaseRoot(sub string) string {
	return filepath.Join(s.root, filepath.FromSlash(stripDigestRoot(render.ReleaseRoot("", sub))))
}

// stripDigestRoot undoes the default "digests" prefix that OutputRoot injects
// when called with an empty root, since s.root already is that directory.
func stripDigestRoot(p string) string {
	const def = "digests/"
	if len(p) > len(def) && p[:len(def)] == def {
		return p[len(def):]
	}
	return p
}

// Release loads one period's release digest.
func (s *Store) Release(sub, period, date string) (*contract.ReleaseDigest, error) {
	if !dateRe.MatchString(date) {
		return nil, ErrReleasesNotFound
	}
	if period == "" {
		period = render.PeriodDaily
	}
	if !validPeriod(period) {
		return nil, ErrReleasesNotFound
	}
	p := filepath.Join(s.releaseRoot(sub), period, render.BucketOf(period, date), render.NameOf(period, date)+".json")
	return readReleaseFile(p)
}

// ReleaseManifest lists available release digests, newest first. An empty sub
// keeps the default "releases" directory. An empty period scans all periods.
func (s *Store) ReleaseManifest(sub, period string) ([]releaseManifestEntry, error) {
	root := s.releaseRoot(sub)
	var periods []string
	if period == "" {
		periods = []string{render.PeriodDaily, render.PeriodWeekly, render.PeriodMonthly}
	} else if validPeriod(period) {
		periods = []string{period}
	} else {
		return []releaseManifestEntry{}, nil
	}
	out := []releaseManifestEntry{}
	for _, p := range periods {
		dir := filepath.Join(root, p)
		buckets, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, b := range buckets {
			if !b.IsDir() {
				continue
			}
			files, err := os.ReadDir(filepath.Join(dir, b.Name()))
			if err != nil {
				continue
			}
			for _, f := range files {
				if f.IsDir() || filepath.Ext(f.Name()) != ".json" {
					continue
				}
				d, err := readReleaseFile(filepath.Join(dir, b.Name(), f.Name()))
				if err != nil {
					continue
				}
				out = append(out, releaseManifestEntry{
					Date:        d.Date,
					Period:      d.Period,
					Count:       d.Count,
					Degraded:    d.Degraded,
					ContentHash: d.ContentHash,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date > out[j].Date
		}
		return out[i].Period > out[j].Period
	})
	return out, nil
}

// Subscriptions reads the subscription list the core will poll. A missing or
// empty file yields an empty list, never an error.
func (s *Store) Subscriptions(path string) (subscriptionList, error) {
	out := subscriptionList{Repos: []string{}}
	if path == "" {
		return out, nil
	}
	st, err := subscription.Load(path)
	if err != nil {
		return out, err
	}
	if repos := st.List(); len(repos) > 0 {
		out.Repos = repos
	}
	return out, nil
}

type releaseManifestEntry struct {
	Date        string `json:"date"`
	Period      string `json:"period"`
	Count       int    `json:"count"`
	Degraded    bool   `json:"degraded"`
	ContentHash string `json:"contentHash"`
}

type subscriptionList struct {
	Repos []string `json:"repos"`
}

// readReleaseFile decodes a release digest document into the contract type, so
// serve, the writer and the plugin all share one shape.
func readReleaseFile(p string) (*contract.ReleaseDigest, error) {
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, ErrReleasesNotFound
	}
	if err != nil {
		return nil, err
	}
	var doc contract.ReleaseDigest
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// handleListReleases serves GET /api/v2/releases.
func (s *Server) handleListReleases(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	if period != "" && !validPeriod(period) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid period", "period": period})
		return
	}
	entries, err := s.store.ReleaseManifest(s.releasesSubdir, period)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

// handleGetReleases serves GET /api/v2/releases/{date}.
func (s *Server) handleGetReleases(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	period := r.URL.Query().Get("period")
	if period != "" && !validPeriod(period) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid period", "period": period})
		return
	}
	doc, err := s.store.Release(s.releasesSubdir, period, date)
	if errors.Is(err, ErrReleasesNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "releases not found", "date": date})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// handleListSubscriptions serves GET /api/v2/subscriptions (read-only).
func (s *Server) handleListSubscriptions(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Subscriptions(s.subscriptionsPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}
