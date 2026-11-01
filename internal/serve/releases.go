package serve

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	contract "github.com/morehao/trendradar/contract"
	"github.com/morehao/trendradar/internal/releasedoc"
	"github.com/morehao/trendradar/internal/releasewatch"
	"github.com/morehao/trendradar/internal/render"
	"github.com/morehao/trendradar/internal/subscription"
)

// ErrReleasesNotFound is returned when a release digest for the requested date
// is absent.
var ErrReleasesNotFound = errors.New("releases not found")

// ErrBadRepo is returned for an owner/repo that cannot name a release document.
var ErrBadRepo = errors.New("invalid repo")

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

// Release loads one period's release digest. Weekly digests written by 0.7.0+
// live under "_weekly" while 0.6.x wrote "weekly", so both locations are tried
// and the new one wins.
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
	for _, dir := range render.ReleaseDirs(period) {
		p := filepath.Join(s.releaseRoot(sub), dir, render.BucketOf(period, date), render.NameOf(period, date)+".json")
		doc, err := readReleaseFile(p)
		if err == nil {
			return doc, nil
		}
		if !errors.Is(err, ErrReleasesNotFound) {
			return nil, err
		}
	}
	return nil, ErrReleasesNotFound
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
	seen := map[string]bool{}
	for _, p := range periods {
		// Same rule as Release: scan the current directory plus the pre-0.7 one.
		for _, dirName := range render.ReleaseDirs(p) {
			dir := filepath.Join(root, dirName)
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
					// One entry per period+date even if both directories have a copy.
					key := p + "\x00" + f.Name()
					if seen[key] {
						continue
					}
					d, err := readReleaseFile(filepath.Join(dir, b.Name(), f.Name()))
					if err != nil {
						continue
					}
					seen[key] = true
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
//
// Only the repo names are read here; the per-repo items are added by the server
// (see handleListSubscriptions) because they need the release cursor path, which
// is a server-level concern (it lives under the data repo's .git).
func (s *Store) Subscriptions(path string) (contract.SubscriptionList, error) {
	out := contract.SubscriptionList{Repos: []string{}}
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

// SubscriptionDoc reads one volume of a repo's release document. The returned
// markdown is the file content verbatim, which is what makes the HTTP mirror and
// the IPC command byte-identical (A10).
func (s *Store) SubscriptionDoc(sub, repo string, volume int) (contract.SubscriptionDoc, error) {
	if !releasedoc.ValidRepo(repo) {
		return contract.SubscriptionDoc{}, ErrBadRepo
	}
	return releasedoc.Read("", s.releaseRoot(sub), repo, volume)
}

// subscriptionItems builds the per-repo rows for the subscriptions endpoint from
// the local cursor plus the committed document index. A cursor that cannot be
// read degrades to baseline rows instead of failing the whole endpoint.
func (s *Store) subscriptionItems(sub string, repos []string, statePath string) []contract.SubscriptionItem {
	return releasewatch.Items(statePath, "", s.releaseRoot(sub), repos)
}

type releaseManifestEntry struct {
	Date        string `json:"date"`
	Period      string `json:"period"`
	Count       int    `json:"count"`
	Degraded    bool   `json:"degraded"`
	ContentHash string `json:"contentHash"`
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

// handleListSubscriptions serves GET /api/v2/subscriptions (read-only). It is
// the HTTP mirror of the IPC `subscribe list` command: same SubscriptionList,
// same items[].
func (s *Server) handleListSubscriptions(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Subscriptions(s.subscriptionsPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out.Items = s.store.subscriptionItems(s.releasesSubdir, out.Repos, s.releaseStatePath)
	writeJSON(w, http.StatusOK, out)
}

// handleSubscriptionDoc serves GET /api/v2/repos/{owner}/{repo}/releases, the
// HTTP mirror of the IPC `subscribe releases` command.
func (s *Server) handleSubscriptionDoc(w http.ResponseWriter, r *http.Request) {
	repo := r.PathValue("owner") + "/" + r.PathValue("repo")
	if !releasedoc.ValidRepo(repo) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid owner/repo", "repo": repo})
		return
	}
	volume, verr := releasedoc.ParseVolume(r.URL.Query().Get("volume"))
	if verr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": verr.Error()})
		return
	}
	doc, err := s.store.SubscriptionDoc(s.releasesSubdir, repo, volume)
	switch {
	case errors.Is(err, releasedoc.ErrNoDoc):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "release document not found", "repo": repo})
		return
	case errors.Is(err, releasedoc.ErrVolumeOutOfRange):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "volume not found", "repo": repo, "volume": strconv.Itoa(volume)})
		return
	case errors.Is(err, ErrBadRepo):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid owner/repo", "repo": repo})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, doc)
}
