package render

import (
	"fmt"
	"strconv"
	"time"

	"github.com/morehao/trendradar/internal/digest"
)

// Supported trending periods. Any other value is normalized to daily.
const (
	PeriodDaily   = "daily"
	PeriodWeekly  = "weekly"
	PeriodMonthly = "monthly"
)

// PeriodOf returns d.Period when it is one of daily|weekly|monthly, otherwise
// "daily". It is the single guard against an out-of-range period, so the rest of
// the pipeline can assume a valid period.
func PeriodOf(d digest.Digest) string {
	switch d.Period {
	case PeriodDaily, PeriodWeekly, PeriodMonthly:
		return d.Period
	default:
		return PeriodDaily
	}
}

// BucketOf returns the directory bucket for a (period, date):
//
//	daily   -> YYYY-MM   (date[:7])
//	monthly -> YYYY      (date[:4])
//	weekly  -> ISO year  (e.g. 2026)
//
// It never panics on a short or malformed date; such a date falls back to the
// raw date string so callers stay safe even on bad input.
func BucketOf(period, date string) string {
	switch period {
	case PeriodMonthly:
		if len(date) >= 4 {
			return date[:4]
		}
		return date
	case PeriodWeekly:
		if y, _, ok := isoWeek(date); ok {
			return strconv.Itoa(y)
		}
		return date
	default: // daily
		if len(date) >= 7 {
			return date[:7]
		}
		return date
	}
}

// NameOf returns the artifact base name for a (period, date):
//
//	daily   -> date      (YYYY-MM-DD)
//	monthly -> YYYY-MM   (date[:7])
//	weekly  -> YYYY-Wnn  (e.g. 2026-W44)
//
// Like BucketOf it degrades to the raw date on malformed input instead of
// panicking.
func NameOf(period, date string) string {
	switch period {
	case PeriodMonthly:
		if len(date) >= 7 {
			return date[:7]
		}
		return date
	case PeriodWeekly:
		if y, w, ok := isoWeek(date); ok {
			return fmt.Sprintf("%d-W%02d", y, w)
		}
		return date
	default: // daily
		return date
	}
}

// isoWeek parses an ISO date and returns its ISO year and week. ok is false when
// date is not a valid YYYY-MM-DD. The ISO year (not the calendar year) is what
// buckets a week: e.g. 2025-12-29 belongs to ISO year 2026.
func isoWeek(date string) (year, week int, ok bool) {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 0, 0, false
	}
	y, w := t.ISOWeek()
	return y, w, true
}
