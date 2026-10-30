package serve

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/http"
)

// Server is the HTTP handler set for trendradar serve.
type Server struct {
	store   *Store
	handler http.Handler
}

// New wires routes for the given store.
func New(store *Store) *Server {
	s := &Server{store: store}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/digests/{date}", s.handleDigest)
	mux.HandleFunc("GET /api/v1/manifest", s.handleManifest)
	mux.HandleFunc("GET /feed.xml", s.handleFeed)
	mux.Handle("GET /", http.FileServer(http.FS(webSub())))
	s.handler = mux
	return s
}

// Handler returns the root handler.
func (s *Server) Handler() http.Handler { return s.handler }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleDigest(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	d, err := s.store.Digest(date)
	if errors.Is(err, ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "digest not found", "date": date})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	m, err := s.store.Manifest()
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
	m, err := s.store.Manifest()
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
			Title: e.Date, Link: "/api/v1/digests/" + e.Date, GUID: e.ContentHash, PubDate: e.Date,
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
