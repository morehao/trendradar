package serve

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func writeWeeklyFixture(t *testing.T, root, dir, name string, date string) {
	t.Helper()
	p := filepath.Join(root, "releases", dir, "2026", name+".json")
	writeJSONFile(t, p, map[string]any{
		"schema": 2, "date": date, "period": "weekly", "count": 1, "degraded": false,
		"contentHash":     "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		"contentHashAlgo": "jcs-sha256-v1", "repos": []any{},
	})
}

// Weekly digests written by 0.7.0+ live under "_weekly" so the directory cannot
// collide with a subscribed owner named "weekly".
func TestWeeklyDigestLivesUnderUnderscoreDir(t *testing.T) {
	root := t.TempDir()
	writeWeeklyFixture(t, root, "_weekly", "2026-W44", "2026-11-01")
	srv := New(NewStore(root)).WithReleases("releases", "")

	rec, body := getJSON(t, srv.Handler(), "/api/v2/releases/2026-11-01?period=weekly")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%v", rec.Code, body)
	}
	if _, ok := body.(map[string]any); !ok {
		t.Fatalf("body=%v", body)
	}
	rec, list := getJSON(t, srv.Handler(), "/api/v2/releases?period=weekly")
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest status=%d", rec.Code)
	}
	arr, ok := list.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("manifest=%v, want 1 entry", list)
	}
}

// Upgrading must not hide history: a weekly digest written by 0.6.x sits in
// "weekly/" and still has to be served and listed.
func TestLegacyWeeklyDirStillServed(t *testing.T) {
	root := t.TempDir()
	writeWeeklyFixture(t, root, "weekly", "2026-W43", "2026-10-25")
	srv := New(NewStore(root)).WithReleases("releases", "")

	rec, _ := getJSON(t, srv.Handler(), "/api/v2/releases/2026-10-25?period=weekly")
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy weekly status=%d, want 200", rec.Code)
	}
	_, list := getJSON(t, srv.Handler(), "/api/v2/releases?period=weekly")
	arr, ok := list.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("manifest=%v, want the legacy entry", list)
	}
}

// When both locations hold the same week (half-migrated repo), the manifest must
// list it once, not twice.
func TestWeeklyManifestDedupesAcrossDirs(t *testing.T) {
	root := t.TempDir()
	writeWeeklyFixture(t, root, "_weekly", "2026-W44", "2026-11-01")
	writeWeeklyFixture(t, root, "weekly", "2026-W44", "2026-11-01")
	srv := New(NewStore(root)).WithReleases("releases", "")

	_, list := getJSON(t, srv.Handler(), "/api/v2/releases?period=weekly")
	arr, ok := list.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("manifest=%v, want a single deduped entry", list)
	}
}

// An owner directory literally named "weekly" must not be mistaken for the
// aggregate directory: only bucket subdirectories are scanned, so its markdown
// documents are ignored and the index stays clean.
func TestOwnerNamedWeeklyDoesNotBreakManifest(t *testing.T) {
	root := t.TempDir()
	writeWeeklyFixture(t, root, "weekly", "2026-W43", "2026-10-25")
	ownerDir := filepath.Join(root, "releases", "weekly")
	if err := os.WriteFile(filepath.Join(ownerDir, "r-01.md"), []byte("# weekly/r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := New(NewStore(root)).WithReleases("releases", "")
	rec, list := getJSON(t, srv.Handler(), "/api/v2/releases?period=weekly")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if arr, ok := list.([]any); !ok || len(arr) != 1 {
		t.Fatalf("manifest=%v, want only the digest entry", list)
	}
}
