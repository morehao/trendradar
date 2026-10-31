package releasewatch

import (
	"fmt"
	"time"
)

// Week is one ISO-8601 week in the configured timezone. Key is used only for
// de-duplication (2026-W44); Start/End are the human-facing period bounds that
// end up in documents and digests.
type Week struct {
	Key   string
	Start string // Monday, YYYY-MM-DD
	End   string // Sunday, YYYY-MM-DD
}

// Location resolves the IANA timezone used to decide week boundaries. An empty
// name means Asia/Shanghai, the default deployment timezone.
func Location(name string) (*time.Location, error) {
	if name == "" {
		name = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", name, err)
	}
	return loc, nil
}

// WeekOf returns the ISO week containing date (YYYY-MM-DD) in loc. Weeks always
// start on Monday: the ISO week number is defined that way, and a summary week
// whose label disagrees with its own key would let the same week be recorded
// twice across a year boundary.
func WeekOf(date string, loc *time.Location) (Week, error) {
	if loc == nil {
		var err error
		loc, err = Location("")
		if err != nil {
			return Week{}, err
		}
	}
	t, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return Week{}, fmt.Errorf("invalid date %q: %w", date, err)
	}
	// Go's Weekday is Sunday=0; shift so Monday=0.
	offset := (int(t.Weekday()) + 6) % 7
	start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -offset)
	end := start.AddDate(0, 0, 6)
	year, week := start.ISOWeek()
	return Week{
		Key:   fmt.Sprintf("%d-W%02d", year, week),
		Start: start.Format("2006-01-02"),
		End:   end.Format("2006-01-02"),
	}, nil
}
