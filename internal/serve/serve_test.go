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

	contract "github.com/morehao/trendradar/contract"
	"github.com/morehao/trendradar/internal/digest"
)

// seed writes a legacy v1 digest file (schema 1, no contentHashAlgo) to exercise
// the historical normalization path.
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

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestDigestNormalizesV1(t *testing.T) {
	h := New(NewStore(seed(t))).Handler()
	rec := get(h, "/api/v2/digests/2026-10-30")
	if rec.Code != http.StatusOK {
		t.Fatalf("digest status = %d", rec.Code)
	}
	var got contract.Digest
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode digest: %v", err)
	}
	if got.Date != "2026-10-30" || got.Schema != 2 {
		t.Fatalf("date=%s schema=%d, want 2026-10-30 / 2", got.Date, got.Schema)
	}
	if got.ContentHashAlgo != "legacy-struct-marshal-v1" {
		t.Fatalf("contentHashAlgo = %q, want legacy", got.ContentHashAlgo)
	}
	if got.ContentHash != "sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("contentHash not preserved: %s", got.ContentHash)
	}
}

func TestV1RedirectsToV2(t *testing.T) {
	h := New(NewStore(seed(t))).Handler()
	rec := get(h, "/api/v1/digests/2026-10-30")
	if rec.Code != http.StatusFound {
		t.Fatalf("v1 status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/api/v2/digests/2026-10-30" {
		t.Fatalf("redirect = %q", loc)
	}
}

func TestMissingAndManifestAndFeedAndIndex(t *testing.T) {
	h := New(NewStore(seed(t))).Handler()

	if rec := get(h, "/api/v2/digests/2099-01-01"); rec.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, want 404", rec.Code)
	}
	rec := get(h, "/api/v2/manifest")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "2026-10-30") || !strings.Contains(rec.Body.String(), "contentHashAlgo") {
		t.Fatalf("manifest = %d %s", rec.Code, rec.Body.String())
	}
	rec = get(h, "/feed.xml")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<rss") || !strings.Contains(rec.Body.String(), "/api/v2/digests/") {
		t.Fatalf("feed = %d %s", rec.Code, rec.Body.String())
	}
	rec = get(h, "/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "trendradar") {
		t.Fatalf("index = %d", rec.Code)
	}
}
