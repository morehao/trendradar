package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	contract "github.com/morehao/trendradar/contract"
)

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// seed writes a per-period layout under daily/weekly/monthly. The daily file is a
// legacy v1 digest (schema 1, no contentHashAlgo) to exercise historical
// normalization; weekly/monthly are v2 fixtures.
func seed(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeJSONFile(t, filepath.Join(root, "daily", "2026-10", "2026-10-30.json"), map[string]any{
		"schema": 1, "date": "2026-10-30", "period": "daily", "languages": []string{"all", "go"},
		"count": 1, "degraded": false, "contentHash": "sha256:" + strings.Repeat("a", 64),
		"coreVersion": "0.1.0", "generatedAt": "2026-10-30T00:05:00Z", "repos": []any{},
	})
	writeJSONFile(t, filepath.Join(root, "weekly", "2026", "2026-W44.json"), map[string]any{
		"schema": 2, "date": "2026-11-01", "period": "weekly", "languages": []string{"all"},
		"count": 1, "degraded": false, "contentHash": "sha256:" + strings.Repeat("b", 64),
		"contentHashAlgo": "jcs-sha256-v1", "repos": []any{},
	})
	writeJSONFile(t, filepath.Join(root, "monthly", "2026", "2026-10.json"), map[string]any{
		"schema": 2, "date": "2026-10-31", "period": "monthly", "languages": []string{"all"},
		"count": 1, "degraded": false, "contentHash": "sha256:" + strings.Repeat("c", 64),
		"contentHashAlgo": "jcs-sha256-v1", "repos": []any{},
	})
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

// The {period}/{date} route resolves weekly/monthly buckets, and the daily form
// of the same route works too.
func TestDigestByPeriod(t *testing.T) {
	h := New(NewStore(seed(t))).Handler()
	cases := []struct {
		path     string
		wantDate string
		wantPer  string
	}{
		{"/api/v2/digests/weekly/2026-11-01", "2026-11-01", "weekly"},
		{"/api/v2/digests/monthly/2026-10-31", "2026-10-31", "monthly"},
		{"/api/v2/digests/daily/2026-10-30", "2026-10-30", "daily"},
	}
	for _, c := range cases {
		rec := get(h, c.path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", c.path, rec.Code, rec.Body.String())
		}
		var got contract.Digest
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s decode: %v", c.path, err)
		}
		if got.Date != c.wantDate || got.Period != c.wantPer {
			t.Fatalf("%s => date=%s period=%s, want %s/%s", c.path, got.Date, got.Period, c.wantDate, c.wantPer)
		}
	}
}

func TestManifestFilter(t *testing.T) {
	h := New(NewStore(seed(t))).Handler()

	all := get(h, "/api/v2/manifest")
	if all.Code != http.StatusOK {
		t.Fatalf("manifest status = %d", all.Code)
	}
	for _, want := range []string{"2026-10-30", "2026-11-01", "2026-10-31", `"period":"daily"`, `"period":"weekly"`, `"period":"monthly"`} {
		if !strings.Contains(all.Body.String(), want) {
			t.Fatalf("full manifest missing %q: %s", want, all.Body.String())
		}
	}

	weekly := get(h, "/api/v2/manifest?period=weekly")
	if weekly.Code != http.StatusOK {
		t.Fatalf("filtered manifest status = %d", weekly.Code)
	}
	if !strings.Contains(weekly.Body.String(), "2026-11-01") || !strings.Contains(weekly.Body.String(), `"period":"weekly"`) {
		t.Fatalf("weekly manifest missing weekly entry: %s", weekly.Body.String())
	}
	if strings.Contains(weekly.Body.String(), "2026-10-30") || strings.Contains(weekly.Body.String(), "2026-10-31") {
		t.Fatalf("weekly manifest leaked other periods: %s", weekly.Body.String())
	}
}

func TestInvalidPeriod400(t *testing.T) {
	h := New(NewStore(seed(t))).Handler()
	rec := get(h, "/api/v2/digests/bogus/2026-10-30")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid period status = %d, want 400", rec.Code)
	}
}

func TestV1Gone(t *testing.T) {
	h := New(NewStore(seed(t))).Handler()
	for _, p := range []string{"/api/v1/digests/2026-10-30", "/api/v1/manifest"} {
		if rec := get(h, p); rec.Code != http.StatusGone {
			t.Fatalf("%s status = %d, want 410", p, rec.Code)
		}
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
	// feed must stay daily-only even though weekly/monthly data exists.
	if strings.Contains(rec.Body.String(), "2026-11-01") {
		t.Fatalf("feed leaked a non-daily entry: %s", rec.Body.String())
	}
	rec = get(h, "/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "trendradar") {
		t.Fatalf("index = %d", rec.Code)
	}
}
