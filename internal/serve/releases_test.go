package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writeReleaseFixture(t *testing.T, root, date string, doc map[string]any) {
	t.Helper()
	dir := filepath.Join(root, "releases", "daily", date[:7])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, date+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func newReleaseServer(root, subdir, subs string) *Server {
	return New(NewStore(root)).WithReleases(subdir, subs)
}

func getJSON(t *testing.T, h http.Handler, path string) (*httptest.ResponseRecorder, any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var v any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &v)
	}
	return rec, v
}

func TestListReleasesEmpty(t *testing.T) {
	srv := newReleaseServer(t.TempDir(), "releases", "")
	rec, body := getJSON(t, srv.Handler(), "/api/v2/releases")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	arr, ok := body.([]any)
	if !ok || len(arr) != 0 {
		t.Fatalf("body = %v, want empty array (not null/404)", body)
	}
}

func TestListReleasesEntries(t *testing.T) {
	root := t.TempDir()
	hash := "sha256:" + "a" + string(make([]byte, 0))
	_ = hash
	writeReleaseFixture(t, root, "2026-10-31", map[string]any{
		"schema": 1, "date": "2026-10-31", "period": "daily",
		"contentHash":     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"contentHashAlgo": "jcs-sha256-v1",
		"count":           2, "degraded": false, "repos": []any{},
	})
	srv := newReleaseServer(root, "releases", "")
	rec, body := getJSON(t, srv.Handler(), "/api/v2/releases")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	arr := body.([]any)
	if len(arr) != 1 {
		t.Fatalf("entries = %d, want 1", len(arr))
	}
	e := arr[0].(map[string]any)
	if e["date"] != "2026-10-31" || e["period"] != "daily" {
		t.Fatalf("entry = %v", e)
	}
	if _, hasCost := e["usage"]; hasCost {
		t.Fatal("manifest must not carry token cost")
	}
}

func TestGetReleases(t *testing.T) {
	root := t.TempDir()
	writeReleaseFixture(t, root, "2026-10-31", map[string]any{
		"schema": 1, "date": "2026-10-31", "period": "daily",
		"contentHash":     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"contentHashAlgo": "jcs-sha256-v1",
		"count":           1, "degraded": false,
		"repos": []any{map[string]any{
			"fullName": "o/r", "url": "https://github.com/o/r", "latestTag": "v1", "newCount": 1,
			"releases": []any{map[string]any{
				"id": 0, "tag": "v1", "name": "v1", "kind": "tag",
				"url":        "https://github.com/o/r/tree/v1",
				"prerelease": false, "draft": false,
				"summaryZh": "x", "summarySource": "model",
			}},
		}},
	})
	srv := newReleaseServer(root, "releases", "")
	rec, body := getJSON(t, srv.Handler(), "/api/v2/releases/2026-10-31")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	doc := body.(map[string]any)
	if doc["date"] != "2026-10-31" {
		t.Fatalf("doc = %v", doc)
	}
	repos := doc["repos"].([]any)
	if len(repos) != 1 {
		t.Fatalf("repos = %v", repos)
	}
}

func TestGetReleasesNotFound(t *testing.T) {
	srv := newReleaseServer(t.TempDir(), "releases", "")
	rec, _ := getJSON(t, srv.Handler(), "/api/v2/releases/2026-01-01")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	// malformed date behaves like missing (mirrors digest behaviour)
	rec2, _ := getJSON(t, srv.Handler(), "/api/v2/releases/not-a-date")
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("malformed date status = %d, want 404", rec2.Code)
	}
}

func TestListSubscriptions(t *testing.T) {
	dir := t.TempDir()
	subs := filepath.Join(dir, "subscriptions.yaml")
	if err := os.WriteFile(subs, []byte("schema: 1\nsubscriptions:\n  - morehao/trendradar\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := newReleaseServer(dir, "releases", subs)
	rec, body := getJSON(t, srv.Handler(), "/api/v2/subscriptions")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	m := body.(map[string]any)
	repos := m["repos"].([]any)
	if len(repos) != 1 || repos[0] != "morehao/trendradar" {
		t.Fatalf("repos = %v", repos)
	}
}

func TestListSubscriptionsMissingFile(t *testing.T) {
	dir := t.TempDir()
	srv := newReleaseServer(dir, "releases", filepath.Join(dir, "nope.yaml"))
	rec, body := getJSON(t, srv.Handler(), "/api/v2/subscriptions")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	m := body.(map[string]any)
	repos, ok := m["repos"].([]any)
	if !ok || len(repos) != 0 {
		t.Fatalf("repos = %v, want empty array", m["repos"])
	}
}

func TestReleasesPeriodValidation(t *testing.T) {
	srv := newReleaseServer(t.TempDir(), "releases", "")
	rec, _ := getJSON(t, srv.Handler(), "/api/v2/releases?period=hourly")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
