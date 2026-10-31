package render

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/morehao/trendradar/internal/digest"
)

func writeDigest(t *testing.T, path string, d digest.Digest) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListDigests(t *testing.T) {
	root := t.TempDir()
	// daily: three dates across two month buckets.
	writeDigest(t, filepath.Join(root, "daily", "2026-09", "2026-09-30.json"), digest.Digest{Date: "2026-09-30", Period: "daily", Count: 1})
	writeDigest(t, filepath.Join(root, "daily", "2026-10", "2026-10-30.json"), digest.Digest{Date: "2026-10-30", Period: "daily", Count: 2})
	writeDigest(t, filepath.Join(root, "daily", "2026-10", "2026-10-31.json"), digest.Digest{Date: "2026-10-31", Period: "daily", Count: 3})
	// weekly / monthly.
	writeDigest(t, filepath.Join(root, "weekly", "2026", "2026-W44.json"), digest.Digest{Date: "2026-11-01", Period: "weekly", Count: 4})
	writeDigest(t, filepath.Join(root, "monthly", "2026", "2026-09.json"), digest.Digest{Date: "2026-09-30", Period: "monthly", Count: 5})
	writeDigest(t, filepath.Join(root, "monthly", "2026", "2026-10.json"), digest.Digest{Date: "2026-10-31", Period: "monthly", Count: 6})
	// Noise that must be ignored: wrong pattern, non-json, missing bucket dir.
	writeDigest(t, filepath.Join(root, "daily", "2026-10", "2026-9-30.json"), digest.Digest{Date: "2026-09-30", Period: "daily"})
	if err := os.WriteFile(filepath.Join(root, "daily", "2026-10", "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	limits := map[string]int{"daily": 2, "weekly": 12, "monthly": 12}
	got, err := ListDigests(root, limits)
	if err != nil {
		t.Fatal(err)
	}
	// Orders: daily newest-first (limit 2), then weekly, then monthly.
	wantDates := []string{"2026-10-31", "2026-10-30", "2026-11-01", "2026-10-31", "2026-09-30"}
	if len(got) != len(wantDates) {
		t.Fatalf("got %d digests, want %d: %+v", len(got), len(wantDates), got)
	}
	for i, want := range wantDates {
		if got[i].Date != want {
			t.Fatalf("digest[%d].Date = %q, want %q (all=%v)", i, got[i].Date, want, datesOf(got))
		}
	}
}

func TestListDigestsMissingRootIsEmpty(t *testing.T) {
	got, err := ListDigests(filepath.Join(t.TempDir(), "nope"), map[string]int{"daily": 90})
	if err != nil {
		t.Fatalf("missing root should not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d digests, want 0", len(got))
	}
}

func datesOf(ds []digest.Digest) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Date
	}
	return out
}
