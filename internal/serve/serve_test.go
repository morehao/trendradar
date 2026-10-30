package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morehao/trendradar/internal/digest"
)

func seed(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "2026-10-30")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	d := digest.Digest{
		Schema: 1, Date: "2026-10-30", Period: "daily", Languages: []string{"all", "go"},
		Count: 1, Degraded: false, ContentHash: "sha256:" + strings.Repeat("a", 64),
		CoreVersion: "0.1.0", GeneratedAt: time.Date(2026, 10, 30, 0, 5, 0, 0, time.UTC),
		Repos: []digest.RepoDigest{},
	}
	b, _ := json.Marshal(d)
	if err := os.WriteFile(filepath.Join(dir, "2026-10-30.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDigestManifestFeed(t *testing.T) {
	h := New(NewStore(seed(t))).Handler()

	// digest ok
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/digests/2026-10-30", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("digest status = %d", rec.Code)
	}
	var got digest.Digest
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode digest: %v", err)
	}
	if got.Date != "2026-10-30" {
		t.Fatalf("date = %s", got.Date)
	}

	// missing digest -> 404
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/digests/2099-01-01", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, want 404", rec.Code)
	}

	// manifest
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/manifest", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "2026-10-30") {
		t.Fatalf("manifest = %d %s", rec.Code, rec.Body.String())
	}

	// feed
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/feed.xml", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<rss") {
		t.Fatalf("feed = %d %s", rec.Code, rec.Body.String())
	}

	// index page (embedded)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "trendradar") {
		t.Fatalf("index = %d", rec.Code)
	}
}
