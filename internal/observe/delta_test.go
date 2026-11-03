package observe

import (
	"testing"
	"time"
)

// wantInt
func wantInt(t *testing.T, label string, d Delta, want *int, wantCov Coverage) {
	t.Helper()
	if d.Coverage != wantCov {
		t.Fatalf("%s coverage = %q, want %q", label, d.Coverage, wantCov)
	}
	if want == nil {
		if d.Value != nil {
			t.Fatalf("%s value = %v, want nil", label, *d.Value)
		}
		return
	}
	if d.Value == nil {
		t.Fatalf("%s value = nil, want %d", label, *want)
	}
	if *d.Value != *want {
		t.Fatalf("%s value = %d, want %d", label, *d.Value, *want)
	}
}

func intp(i int) *int { return &i }

// TestComputeNormal: contiguous daily data yields exact complete baselines.
func TestComputeNormal(t *testing.T) {
	snaps := []Snapshot{
		{RepoID: "1", Date: "2026-10-03", Stars: 50},  // d30 base for 2026-11-02 (ideal 2026-10-03)
		{RepoID: "1", Date: "2026-10-26", Stars: 90},  // d7 base (ideal 2026-10-26)
		{RepoID: "1", Date: "2026-11-01", Stars: 100}, // d1 base (ideal 2026-11-01)
		{RepoID: "1", Date: "2026-11-02", Stars: 120},
	}
	got := computeDeltas(snaps, "2026-11-02")
	wantInt(t, "d1", got.D1, intp(20), CoverageComplete)
	wantInt(t, "d7", got.D7, intp(30), CoverageComplete)
	wantInt(t, "d30", got.D30, intp(70), CoverageComplete)
	if got.D1.Days != 1 || got.D7.Days != 7 || got.D30.Days != 30 {
		t.Fatalf("days = %d/%d/%d, want 1/7/30", got.D1.Days, got.D7.Days, got.D30.Days)
	}
}

// TestComputeMissing1Day: the ideal d1 base is absent but exists one day earlier
// (within lookback) -> partial, using the real elapsed interval.
func TestComputeMissing1Day(t *testing.T) {
	snaps := []Snapshot{
		{RepoID: "1", Date: "2026-10-31", Stars: 100}, // 2 days before target
		{RepoID: "1", Date: "2026-11-02", Stars: 130},
	}
	got := computeDeltas(snaps, "2026-11-02")
	// d1 ideal base = 2026-11-01 (missing); nearest earlier within 3d = 2026-10-31.
	wantInt(t, "d1", got.D1, intp(30), CoveragePartial)
	if got.D1.Days != 2 {
		t.Fatalf("d1 days = %d, want 2 (actual interval)", got.D1.Days)
	}
	if got.D1.Baseline != "2026-10-31" {
		t.Fatalf("d1 baseline = %s, want 2026-10-31", got.D1.Baseline)
	}
}

// TestComputeMissing3Days: exactly maxLookback slack is still usable (partial).
func TestComputeMissing3Days(t *testing.T) {
	// target 2026-11-05; d1 ideal 2026-11-04 missing, base at 2026-11-01 (3 days slack).
	snaps := []Snapshot{
		{RepoID: "1", Date: "2026-11-01", Stars: 100},
		{RepoID: "1", Date: "2026-11-05", Stars: 160},
	}
	got := computeDeltas(snaps, "2026-11-05")
	wantInt(t, "d1", got.D1, intp(60), CoveragePartial)
	if got.D1.Days != 4 {
		t.Fatalf("d1 days = %d, want 4", got.D1.Days)
	}
}

// TestComputeNoBaseline: no earlier snapshot -> null with reason, never 0.
func TestComputeNoBaseline(t *testing.T) {
	snaps := []Snapshot{
		{RepoID: "1", Date: "2026-11-02", Stars: 100},
	}
	got := computeDeltas(snaps, "2026-11-02")
	for name, d := range map[string]Delta{"d1": got.D1, "d7": got.D7, "d30": got.D30} {
		if d.Value != nil {
			t.Fatalf("%s value = %v, want nil (never 0)", name, *d.Value)
		}
		if d.Coverage != CoverageNoBaseline {
			t.Fatalf("%s coverage = %q, want no-baseline", name, d.Coverage)
		}
		if d.Reason == "" {
			t.Fatalf("%s reason empty, must explain why", name)
		}
	}
}

// TestComputeNoObservation: missing target snapshot -> all no-baseline, no error.
func TestComputeNoObservation(t *testing.T) {
	snaps := []Snapshot{
		{RepoID: "1", Date: "2026-11-01", Stars: 100},
	}
	got := computeDeltas(snaps, "2026-11-02")
	if got.D1.Coverage != CoverageNoBaseline || got.D1.Value != nil {
		t.Fatalf("missing target should be no-baseline null, got %+v", got.D1)
	}
	if got.D1.Reason != "no-observation" {
		t.Fatalf("reason = %q, want no-observation", got.D1.Reason)
	}
}

// TestComputeNegativeGrowth: a star loss stays negative (not clamped to 0).
func TestComputeNegativeGrowth(t *testing.T) {
	snaps := []Snapshot{
		{RepoID: "1", Date: "2026-11-01", Stars: 200},
		{RepoID: "1", Date: "2026-11-02", Stars: 180},
	}
	got := computeDeltas(snaps, "2026-11-02")
	wantInt(t, "d1", got.D1, intp(-20), CoverageComplete)
}

// TestSystemClockToday checks the production clock returns a parseable YYYY-MM-DD.
func TestSystemClockToday(t *testing.T) {
	today := SystemClock{}.Today()
	if len(today) != 10 {
		t.Fatalf("Today() = %q, want 10-char date", today)
	}
	if _, err := time.Parse(dateLayout, today); err != nil {
		t.Fatalf("Today() unparseable: %v", err)
	}
}
