package serve

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	contract "github.com/morehao/trendradar/contract"
	"github.com/morehao/trendradar/internal/releasedoc"
	"github.com/morehao/trendradar/internal/releasewatch"
)

// seedSubscriptions writes the pieces a real checkout has: the versioned
// subscription file, the per-repo document and the machine-local release cursor.
func seedSubscriptions(t *testing.T) (root, subsPath, statePath string) {
	t.Helper()
	root = t.TempDir()
	repoRoot := t.TempDir()
	docRoot := filepath.Join(root, "releases")
	rel, data, need, err := releasedoc.Plan(repoRoot, docRoot, "o/r", releasedoc.Section{
		Repo:      "o/r",
		WeekStart: "2026-10-26",
		WeekEnd:   "2026-11-01",
		SummaryZh: "本周发布 v2，修复若干问题。",
		Items:     []releasedoc.Item{{Tag: "v2", URL: "https://github.com/o/r/releases/tag/v2", Date: "2026-10-29"}},
	}, 600)
	if err != nil || !need {
		t.Fatalf("plan doc: need=%v err=%v", need, err)
	}
	if err := os.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rel, data, 0o644); err != nil {
		t.Fatal(err)
	}

	subsPath = filepath.Join(repoRoot, "digests", "subscriptions.yaml")
	if err := os.MkdirAll(filepath.Dir(subsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(subsPath, []byte("schema: 1\nsubscriptions:\n  - o/r\n  - o/never-checked\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	statePath = filepath.Join(repoRoot, ".git", "trendradar-release-state.json")
	st := releasewatch.NewState()
	checked := time.Date(2026, 11, 1, 0, 30, 0, 0, time.UTC)
	st.Set("o/r", releasewatch.RepoState{
		LastID: 2, LastTag: "v2", Status: releasewatch.StatusOK,
		WeekKey: "2026-W44", LatestSummaryAt: "2026-11-01", LastCheckedAt: &checked,
	})
	if err := st.Save(statePath); err != nil {
		t.Fatal(err)
	}
	return root, subsPath, statePath
}

// A10: the HTTP mirror must return the same SubscriptionDoc as the IPC command.
// Both go through releasedoc.Read, so the assertion is byte-for-byte.
func TestSubscriptionDocMatchesReadPath(t *testing.T) {
	root, subs, state := seedSubscriptions(t)
	h := New(NewStore(root)).WithReleases("releases", subs).WithReleaseState(state).Handler()

	rec := get(h, "/api/v2/repos/o/r/releases")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got contract.SubscriptionDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want, err := releasedoc.Read("", filepath.Join(root, "releases"), "o/r", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("HTTP doc != read path:\n got=%+v\nwant=%+v", got, want)
	}
	if got.Volume != 1 || len(got.Volumes) != 1 || got.Volumes[0].File != "o/r-01.md" {
		t.Fatalf("volume index = %+v", got.Volumes)
	}
	if got.UpdatedAt == nil || *got.UpdatedAt != "2026-11-01" {
		t.Fatalf("updatedAt = %v", got.UpdatedAt)
	}
}

func TestSubscriptionDocErrors(t *testing.T) {
	root, subs, state := seedSubscriptions(t)
	h := New(NewStore(root)).WithReleases("releases", subs).WithReleaseState(state).Handler()

	if rec := get(h, "/api/v2/repos/o/missing/releases"); rec.Code != http.StatusNotFound {
		t.Fatalf("missing doc status=%d, want 404", rec.Code)
	}
	if rec := get(h, "/api/v2/repos/o/r/releases?volume=9"); rec.Code != http.StatusNotFound {
		t.Fatalf("out-of-range volume status=%d, want 404", rec.Code)
	}
	if rec := get(h, "/api/v2/repos/o/r/releases?volume=0"); rec.Code != http.StatusBadRequest {
		t.Fatalf("zero volume status=%d, want 400", rec.Code)
	}
	if rec := get(h, "/api/v2/repos/o/r/releases?volume=x"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad volume status=%d, want 400", rec.Code)
	}
	// A traversal attempt never reaches the filesystem. Go's ServeMux cleans the
	// path with a redirect before the handler runs, which also means no file read.
	if rec := get(h, "/api/v2/repos/o/../releases"); rec.Code == http.StatusOK {
		t.Fatalf("traversal must not succeed: %d", rec.Code)
	}
}

func TestSubscriptionsEndpointCarriesItems(t *testing.T) {
	root, subs, state := seedSubscriptions(t)
	h := New(NewStore(root)).WithReleases("releases", subs).WithReleaseState(state).Handler()

	rec := get(h, "/api/v2/subscriptions")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var got contract.SubscriptionList
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// repos stays the legacy field an old consumer reads.
	if len(got.Repos) != 2 || got.Repos[0] != "o/r" {
		t.Fatalf("repos = %v", got.Repos)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items = %+v, want one per repo", got.Items)
	}
	first := got.Items[0]
	if first.FullName != "o/r" || first.Status != contract.SubscriptionStatusOk {
		t.Fatalf("item = %+v", first)
	}
	if first.LatestTag == nil || *first.LatestTag != "v2" {
		t.Fatalf("latestTag = %v", first.LatestTag)
	}
	if first.LatestSummaryAt == nil || *first.LatestSummaryAt != "2026-11-01" {
		t.Fatalf("latestSummaryAt = %v", first.LatestSummaryAt)
	}
	if first.LastCheckedAt == nil {
		t.Fatal("lastCheckedAt missing")
	}
	// A repo with no cursor yet reports baseline, not a fake "ok".
	if got.Items[1].Status != contract.SubscriptionStatusBaseline {
		t.Fatalf("never-checked status = %q", got.Items[1].Status)
	}
}

// A9: a document written before schema 2 must still be served by the legacy
// route. serve never validates on read, so old files keep working.
func TestLegacyReleaseDigestStillServed(t *testing.T) {
	root := t.TempDir()
	writeReleaseFixture(t, root, "2026-10-31", map[string]any{
		"schema": 1, "date": "2026-10-31", "period": "daily", "count": 1, "degraded": false,
		"contentHash": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"repos": []any{map[string]any{
			"fullName": "o/r", "url": "https://github.com/o/r", "latestTag": "v1", "newCount": 1,
			"releases": []any{map[string]any{
				"id": 1, "tag": "v1", "name": "v1", "kind": "release",
				"url": "https://github.com/o/r/releases/tag/v1", "prerelease": false, "draft": false,
				"summaryZh": "旧文档里的逐条摘要", "summarySource": "model",
			}},
		}},
	})
	srv := New(NewStore(root)).WithReleases("releases", "")
	rec, body := getJSON(t, srv.Handler(), "/api/v2/releases/2026-10-31")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%v", rec.Code, body)
	}
	obj, ok := body.(map[string]any)
	if !ok {
		t.Fatalf("body=%v", body)
	}
	if obj["schema"].(float64) != 1 || obj["date"] != "2026-10-31" {
		t.Fatalf("legacy fields changed: %v", obj)
	}
	// Optional fields absent in the legacy file must not appear as nulls that
	// would confuse an old consumer.
	if _, ok := obj["coreVersion"]; ok {
		t.Fatalf("coreVersion should stay absent: %v", obj)
	}
}
