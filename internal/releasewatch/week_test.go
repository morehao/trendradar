package releasewatch

import "testing"

func TestWeekOfISO8601(t *testing.T) {
	loc, err := Location("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		date       string
		key        string
		start, end string
	}{
		{"2026-11-01", "2026-W44", "2026-10-26", "2026-11-01"}, // Sunday
		{"2026-10-26", "2026-W44", "2026-10-26", "2026-11-01"}, // Monday
		{"2026-10-28", "2026-W44", "2026-10-26", "2026-11-01"}, // mid-week
		// 2027-01-01 is a Friday inside ISO week 2026-W53: the key must stay on
		// 2026, otherwise the same week could be recorded twice across the year
		// boundary.
		{"2027-01-01", "2026-W53", "2026-12-28", "2027-01-03"},
		{"2027-01-04", "2027-W01", "2027-01-04", "2027-01-10"},
	}
	for _, c := range cases {
		got, err := WeekOf(c.date, loc)
		if err != nil {
			t.Fatalf("%s: %v", c.date, err)
		}
		if got.Key != c.key || got.Start != c.start || got.End != c.end {
			t.Fatalf("%s -> %+v, want %s %s..%s", c.date, got, c.key, c.start, c.end)
		}
	}
}

func TestWeekOfRejectsBadDate(t *testing.T) {
	if _, err := WeekOf("2026-13-01", nil); err == nil {
		t.Fatal("want an error for an impossible date")
	}
}

func TestLocationDefaultsToShanghai(t *testing.T) {
	loc, err := Location("")
	if err != nil {
		t.Fatal(err)
	}
	if loc.String() != "Asia/Shanghai" {
		t.Fatalf("default tz = %s", loc)
	}
	if _, err := Location("Mars/Olympus"); err == nil {
		t.Fatal("want an error for an unknown timezone")
	}
}
