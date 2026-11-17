package releasewatch

import (
	"os"
	"path/filepath"
	"testing"

	contract "github.com/morehao/trendradar/contract"
)

// A poisoned cursor (lastTag=nightly, as written by an older core) must be
// healed at read time from the dated weekly digest, so the panel shows the real
// newest version without waiting for a re-scan.
func TestItemsHealsStaleRollingLastTag(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "state.json")
	docRoot := filepath.Join(root, "releases")
	weekDir := filepath.Join(docRoot, "_weekly", "2026")
	if err := os.MkdirAll(weekDir, 0o755); err != nil {
		t.Fatal(err)
	}
	digest := `{"schema":2,"repos":[
	  {"fullName":"infiniflow/ragflow","latestTag":"nightly","releases":[
	    {"tag":"nightly","publishedAt":"2025-12-01T13:17:42Z"},
	    {"tag":"v1.0.0","publishedAt":"2026-11-06T12:31:31Z"}
	  ]}
	]}`
	if err := os.WriteFile(filepath.Join(weekDir, "2026-W47.json"), []byte(digest), 0o644); err != nil {
		t.Fatal(err)
	}

	st := NewState()
	st.Set("infiniflow/ragflow", RepoState{LastTag: "nightly", Status: StatusOK})
	if err := st.Save(statePath); err != nil {
		t.Fatal(err)
	}

	items := Items(statePath, root, docRoot, []string{"infiniflow/ragflow"})
	if len(items) != 1 || items[0].LatestTag == nil {
		t.Fatalf("unexpected items: %+v", items)
	}
	if got := *items[0].LatestTag; got != "v1.0.0" {
		t.Fatalf("LatestTag=%q, want v1.0.0 (healed from digest)", got)
	}

	// When no digest exists, the cursor value is preserved (no regression).
	items2 := Items(statePath, root, filepath.Join(root, "empty"), []string{"infiniflow/ragflow"})
	if *items2[0].LatestTag != "nightly" {
		t.Fatalf("no-digest LatestTag=%q, want nightly unchanged", *items2[0].LatestTag)
	}
}

var _ = contract.SubscriptionItem{}
