package parse

import "github.com/morehao/trendradar/internal/digest"

// Detect aggregates per-page stats into a ParseReport. Degraded is set when the
// missing-field rate exceeds threshold.
func Detect(stats []Stats, threshold float64) digest.ParseReport {
	var rep digest.ParseReport
	for _, st := range stats {
		rep.Total += st.Total
		rep.MissingFullName += st.MissingFullName
		rep.MissingURL += st.MissingURL
	}
	missing := rep.MissingFullName + rep.MissingURL
	if denom := rep.Total * 2; denom > 0 {
		rep.MissingRate = float64(missing) / float64(denom)
	}
	rep.Degraded = rep.MissingRate > threshold
	return rep
}
