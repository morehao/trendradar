package render

import (
	"testing"

	"github.com/morehao/trendradar/internal/digest"
)

func TestPeriodOf(t *testing.T) {
	cases := map[string]string{
		"daily":   "daily",
		"weekly":  "weekly",
		"monthly": "monthly",
		"":        "daily",
		"yearly":  "daily",
		"bogus":   "daily",
	}
	for in, want := range cases {
		if got := PeriodOf(digest.Digest{Period: in}); got != want {
			t.Errorf("PeriodOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBucketAndName(t *testing.T) {
	cases := []struct {
		period     string
		date       string
		wantBucket string
		wantName   string
	}{
		{"daily", "2026-10-30", "2026-10", "2026-10-30"},
		{"weekly", "2026-11-01", "2026", "2026-W44"}, // Sunday -> ISO W44
		{"weekly", "2025-12-29", "2026", "2026-W01"}, // ISO year crosses into 2026
		{"weekly", "2027-01-01", "2026", "2026-W53"}, // ISO year is 2026, not 2027
		{"monthly", "2026-10-31", "2026", "2026-10"},
	}
	for _, c := range cases {
		if got := BucketOf(c.period, c.date); got != c.wantBucket {
			t.Errorf("BucketOf(%q,%q) = %q, want %q", c.period, c.date, got, c.wantBucket)
		}
		if got := NameOf(c.period, c.date); got != c.wantName {
			t.Errorf("NameOf(%q,%q) = %q, want %q", c.period, c.date, got, c.wantName)
		}
	}
}

// Malformed dates must never panic: they degrade to the raw date string.
func TestBucketAndNameMalformed(t *testing.T) {
	for _, period := range []string{"daily", "weekly", "monthly"} {
		for _, date := range []string{"", "2026", "2026-1", "not-a-date"} {
			_ = BucketOf(period, date)
			_ = NameOf(period, date)
		}
	}
	if got := BucketOf("daily", ""); got != "" {
		t.Errorf("daily empty bucket = %q, want empty", got)
	}
	if got := NameOf("weekly", "bad"); got != "bad" {
		t.Errorf("weekly malformed name = %q, want raw date", got)
	}
}
