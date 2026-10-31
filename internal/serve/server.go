package serve

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/http"

	"github.com/morehao/trendradar/internal/render"
)

// Server is the HTTP handler set for trendradar serve.
type Server struct {
	store             *Store
	handler           http.Handler
	releasesSubdir    string // config releases.outputDir (default "releases")
	subscriptionsPath string // absolute path to subscriptions.yaml ("" = none)
}

// WithReleases enables the /api/v2/releases* and /api/v2/subscriptions routes.
// subDir is the releases sub-directory inside the digest root; subsPath is the
// resolved subscription file. Both may be empty, in which case the routes serve
// an empty list rather than erroring.
func (s *Server) WithReleases(subDir, subsPath string) *Server {
	if subDir == "" {
		subDir = "releases"
	}
	s.releasesSubdir = subDir
	s.subscriptionsPath = subsPath
	return s
}

// New wires routes for the given store.
func New(store *Store) *Server {
	s := &Server{store: store}
	mux := http.NewServeMux()
	// Legacy date-only route maps to daily (old behavior preserved).
	mux.HandleFunc("GET /api/v2/digests/{date}", s.handleDigest)
	// New additive route: explicit period.
	mux.HandleFunc("GET /api/v2/digests/{period}/{date}", s.handleDigestByPeriod)
	mux.HandleFunc("GET /api/v2/manifest", s.handleManifest)
	// Release subscriptions (read-only).
	mux.HandleFunc("GET /api/v2/releases", s.handleListReleases)
	mux.HandleFunc("GET /api/v2/releases/{date}", s.handleGetReleases)
	mux.HandleFunc("GET /api/v2/subscriptions", s.handleListSubscriptions)
	// v1 HTTP surface is retired: respond 410 Gone, point clients at v2.
	mux.HandleFunc("GET /api/v1/digests/{date}", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "api v1 removed; use /api/v2/digests/{date}", http.StatusGone)
	})
	mux.HandleFunc("GET /api/v1/manifest", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "api v1 removed; use /api/v2/manifest", http.StatusGone)
	})
	mux.HandleFunc("GET /feed.xml", s.handleFeed)
	mux.Handle("GET /", http.FileServer(http.FS(webSub())))
	s.handler = mux
	return s
}

// validPeriod reports whether p is a supported period.
func validPeriod(p string) bool {
	switch p {
	case render.PeriodDaily, render.PeriodWeekly, render.PeriodMonthly:
		return true
	default:
		return false
	}
}

// Handler returns the root handler.
func (s *Server) Handler() http.Handler { return s.handler }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// handleDigest serves the legacy date-only route, which means the daily digest.
func (s *Server) handleDigest(w http.ResponseWriter, r *http.Request) {
	s.writeDigest(w, r.PathValue("date"), render.PeriodDaily)
}

// handleDigestByPeriod serves the explicit {period}/{date} route.
func (s *Server) handleDigestByPeriod(w http.ResponseWriter, r *http.Request) {
	period := r.PathValue("period")
	if !validPeriod(period) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid period", "period": period})
		return
	}
	s.writeDigest(w, r.PathValue("date"), period)
}

func (s *Server) writeDigest(w http.ResponseWriter, date, period string) {
	d, err := s.store.Digest(period, date)
	if errors.Is(err, ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "digest not found", "date": date, "period": period})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	m, err := s.store.Manifest(r.URL.Query().Get("period"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, m)
}

type rss struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	Channel channel  `xml:"channel"`
}

type channel struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	Items       []item `xml:"item"`
}

type item struct {
	Title   string `xml:"title"`
	Link    string `xml:"link"`
	GUID    string `xml:"guid"`
	PubDate string `xml:"pubDate"`
}

func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request) {
	m, err := s.store.Manifest(render.PeriodDaily)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	feed := rss{Version: "2.0", Channel: channel{Title: "trendradar", Link: "/", Description: "daily trending digests"}}
	for i, e := range m.Entries {
		if i >= 20 {
			break
		}
		feed.Channel.Items = append(feed.Channel.Items, item{
			Title: e.Date, Link: "/api/v2/digests/" + e.Date, GUID: e.ContentHash, PubDate: e.Date,
		})
	}
	out, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	_, _ = w.Write(append([]byte(xml.Header), out...))
}
