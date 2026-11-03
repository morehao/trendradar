package observe

import (
	"time"
)

// Clock is injectable so delta computation is deterministic in tests and never
// depends on the wall clock. Production code passes SystemClock{}.
type Clock interface {
	// Today returns the current UTC date as "YYYY-MM-DD".
	Today() string
}

// SystemClock is the production Clock backed by time.Now in UTC.
type SystemClock struct{}

// Today implements Clock.
func (SystemClock) Today() string { return time.Now().UTC().Format(dateLayout) }

// maxLookback bounds how far before a window's ideal baseline we will search for
// the nearest earlier snapshot. Scanned data has gaps (network outages, days the
// tool did not run), and a hard "exactly N days ago" rule would report
// no-baseline on every hiccup. Three days of slack keeps d1 usable across a
// weekend while still excluding anchors so stale they distort the rate.
const maxLookback = 3

// Coverage describes how complete a delta's baseline is.
type Coverage string

const (
	// CoverageComplete: the baseline is exactly the ideal day (window days before).
	CoverageComplete Coverage = "complete"
	// CoveragePartial: the baseline is within maxLookback days but not exact; the
	// delta is current − baseline over the actual elapsed interval (not normalized).
	CoveragePartial Coverage = "partial"
	// CoverageNoBaseline: no usable earlier snapshot; the delta is null.
	CoverageNoBaseline Coverage = "no-baseline"
)

// Delta is the result of comparing a snapshot to its baseline for one window.
// Value is nil — never 0 — when there is no baseline; the distinction between
// "unknown" and "zero change" is the whole point (plan §3 invariant 2).
type Delta struct {
	Value    *int     `json:"value"`
	Coverage Coverage `json:"coverage"`
	Reason   string   `json:"reason,omitempty"` // set when Coverage is no-baseline
	// Baseline is the date actually used as the lower bound ("" when none), and
	// Days is the real number of days between baseline and target. Exposed so
	// callers can render "last N days" honestly instead of assuming the window.
	Baseline string `json:"baseline,omitempty"`
	Days     int    `json:"days"`
}

// Deltas bundles the three standard windows for one repository on one date.
type Deltas struct {
	D1  Delta `json:"d1"`
	D7  Delta `json:"d7"`
	D30 Delta `json:"d30"`
}

// Compute calculates d1/d7/d30 deltas for repoID on targetDate from snapshots
// already loaded in the store. It returns an error only for I/O problems; a
// missing target snapshot yields all three windows as no-baseline (null), not an
// error, because "we have no observation today" is a normal, expected state.
//
// Negative values are preserved: a repo can lose stars, and hiding that with a
// floor of 0 would misreport the trend. Callers that sort can clamp separately.
func (s *Store) Compute(repoID, targetDate string) (Deltas, error) {
	snaps, err := s.Snapshots(repoID)
	if err != nil {
		return Deltas{}, err
	}
	return computeDeltas(snaps, targetDate), nil
}

// computeDeltas is the pure core: derive d1/d7/d30 from a repo's snapshots. It is
// split out so tests can exercise the edge cases (missing days, no baseline,
// negative growth) without touching disk.
func computeDeltas(snaps []Snapshot, targetDate string) Deltas {
	// Index by date. Snaps are assumed deduplicated (one per date); a later entry
	// for the same date wins, matching the store's last-write-wins semantics.
	byDate := make(map[string]Snapshot, len(snaps))
	for _, snap := range snaps {
		byDate[snap.Date] = snap
	}

	target, ok := byDate[targetDate]
	if !ok {
		return Deltas{
			D1:  noBaseline("no-observation"),
			D7:  noBaseline("no-observation"),
			D30: noBaseline("no-observation"),
		}
	}

	return Deltas{
		D1:  deltaForWindow(byDate, target, targetDate, 1),
		D7:  deltaForWindow(byDate, target, targetDate, 7),
		D30: deltaForWindow(byDate, target, targetDate, 30),
	}
}

// deltaForWindow computes a single window delta: it looks for the nearest
// snapshot on or before (targetDate − window), allowing up to maxLookback days of
// slack. If found, value = target.Stars − baseline.Stars.
func deltaForWindow(byDate map[string]Snapshot, target Snapshot, targetDate string, window int) Delta {
	ideal, err := time.Parse(dateLayout, targetDate)
	if err != nil {
		// Invalid target date cannot index a baseline; treat as no-baseline.
		return noBaseline("invalid-date")
	}
	idealBase := ideal.AddDate(0, 0, -window)

	var (
		found   bool
		base    Snapshot
		baseDay time.Time
	)
	// Search the ideal baseline day first, then progressively *earlier* days (up
	// to maxLookback of slack). Looking only backwards keeps the baseline at or
	// before the ideal boundary, so we never anchor on a day newer than intended
	// (which would invert the direction of the delta).
	for back := 0; back <= maxLookback; back++ {
		d := idealBase.AddDate(0, 0, -back)
		key := d.Format(dateLayout)
		if snap, ok := byDate[key]; ok {
			found, base, baseDay = true, snap, d
			break
		}
	}
	if !found {
		return noBaseline("no-baseline")
	}

	value := target.Stars - base.Stars
	// days is the actual interval between baseline and target (window + any slack),
	// derived from real dates so a partial baseline reports "over N days" honestly.
	days := int(ideal.Sub(baseDay).Hours() / 24)

	cov := CoverageComplete
	if baseDay.Before(idealBase) {
		cov = CoveragePartial
	}
	return Delta{
		Value:    &value,
		Coverage: cov,
		Baseline: base.Date,
		Days:     days,
	}
}

func noBaseline(reason string) Delta {
	return Delta{Value: nil, Coverage: CoverageNoBaseline, Reason: reason}
}
