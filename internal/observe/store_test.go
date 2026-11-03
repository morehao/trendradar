package observe

import (
	"os"
	"path/filepath"
	"testing"
)

// newTestStore opens a store in a fresh temp dir.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

// TestPutSameDayKeepsOneDedupsOnLoad verifies that writing the same (repoId,date)
// twice appends two lines but the loaded view keeps only the newest value.
func TestPutSameDayKeepsOneDedupsOnLoad(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	first := Snapshot{RepoID: "1", Date: "2026-11-01", Stars: 100, Source: "snapshot"}
	second := Snapshot{RepoID: "1", Date: "2026-11-01", Stars: 150, Source: "snapshot"}

	if _, existed, err := s.Put(first); err != nil || existed {
		t.Fatalf("first Put: existed=%v err=%v", existed, err)
	}
	prev, existed, err := s.Put(second)
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	if !existed {
		t.Fatal("second Put should report existing prior snapshot")
	}
	if prev.Stars != 100 {
		t.Fatalf("prior value = %d, want 100", prev.Stars)
	}

	// Both lines are physically present (append-only) before compaction.
	raw, err := os.ReadFile(filepath.Join(dir, "stars-2026-11.jsonl"))
	if err != nil {
		t.Fatalf("read shard: %v", err)
	}
	if got := countLines(raw); got != 2 {
		t.Fatalf("physical lines = %d, want 2 (append-only)", got)
	}

	// A fresh store must see exactly one value: the last write.
	fresh, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	snaps, err := fresh.Snapshots("1")
	if err != nil {
		t.Fatalf("Snapshots: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("snapshots = %d, want 1 (deduped)", len(snaps))
	}
	if snaps[0].Stars != 150 {
		t.Fatalf("deduped value = %d, want 150 (last write wins)", snaps[0].Stars)
	}
}

// TestCompactPhysicallyDedups verifies compaction leaves one line per key.
func TestCompactPhysicallyDedups(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, stars := range []int{10, 20, 30} {
		if _, _, err := s.Put(Snapshot{RepoID: "1", Date: "2026-11-01", Stars: stars}); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	if err := s.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "stars-2026-11.jsonl"))
	if err != nil {
		t.Fatalf("read shard: %v", err)
	}
	if got := countLines(raw); got != 1 {
		t.Fatalf("lines after compact = %d, want 1", got)
	}
	if _, err := os.Stat(filepath.Join(dir, manifestName)); err != nil {
		t.Fatalf("manifest missing after compact: %v", err)
	}
}

// TestCrossMonthShards verifies snapshots land in the month shard of their date.
func TestCrossMonthShards(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rows := []Snapshot{
		{RepoID: "1", Date: "2026-10-31", Stars: 5},
		{RepoID: "1", Date: "2026-11-01", Stars: 6},
		{RepoID: "1", Date: "2026-11-30", Stars: 9},
	}
	for _, r := range rows {
		if _, _, err := s.Put(r); err != nil {
			t.Fatalf("Put %s: %v", r.Date, err)
		}
	}
	for _, m := range []string{"2026-10", "2026-11"} {
		if _, err := os.Stat(filepath.Join(dir, "stars-"+m+".jsonl")); err != nil {
			t.Fatalf("missing shard %s: %v", m, err)
		}
	}
	fresh, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	snaps, err := fresh.Snapshots("1")
	if err != nil {
		t.Fatalf("Snapshots: %v", err)
	}
	if len(snaps) != 3 {
		t.Fatalf("cross-month snapshots = %d, want 3", len(snaps))
	}
	// Sorted ascending by date across shards.
	wantDates := []string{"2026-10-31", "2026-11-01", "2026-11-30"}
	for i, w := range wantDates {
		if snaps[i].Date != w {
			t.Fatalf("snaps[%d].Date = %s, want %s", i, snaps[i].Date, w)
		}
	}
}

// TestCorruptLinesSkippedAndCounted verifies corrupt/garbage lines are skipped
// (not fatal) and counted, while good lines still load.
func TestCorruptLinesSkippedAndCounted(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Seed one good snapshot through the API.
	if _, _, err := s.Put(Snapshot{RepoID: "1", Date: "2026-11-01", Stars: 42}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Append corrupt lines directly, mimicking on-disk corruption.
	f, err := os.OpenFile(filepath.Join(dir, "stars-2026-11.jsonl"), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.WriteString("{not json\n\n{\"repoId\":\"\",\"date\":\"bad\"}\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	fresh, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	snaps, err := fresh.Snapshots("1")
	if err != nil {
		t.Fatalf("Snapshots: %v", err)
	}
	if len(snaps) != 1 || snaps[0].Stars != 42 {
		t.Fatalf("good line not preserved: %+v", snaps)
	}
	// Two corrupt lines: the malformed JSON and the invalid-entity (empty id/bad date).
	if got := fresh.Skipped(); got != 2 {
		t.Fatalf("Skipped = %d, want 2", got)
	}
}

// TestPutRejectsInvalid verifies invalid snapshots are rejected before writing.
func TestPutRejectsInvalid(t *testing.T) {
	s := newTestStore(t)
	cases := []Snapshot{
		{RepoID: "", Date: "2026-11-01"},
		{RepoID: "1", Date: "not-a-date"},
	}
	for i, c := range cases {
		if _, _, err := s.Put(c); err == nil {
			t.Fatalf("case %d: expected error, got nil", i)
		}
	}
}

// TestLookup finds a specific snapshot.
func TestLookup(t *testing.T) {
	s := newTestStore(t)
	if _, _, err := s.Put(Snapshot{RepoID: "9", Date: "2026-11-02", Stars: 7}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok, err := s.Lookup("9", "2026-11-02")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !ok || got.Stars != 7 {
		t.Fatalf("Lookup = %+v ok=%v, want stars 7", got, ok)
	}
	if _, ok, _ := s.Lookup("9", "2026-11-03"); ok {
		t.Fatal("Lookup should miss for absent date")
	}
}

func countLines(b []byte) int {
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	return n
}
