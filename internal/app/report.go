package app

import (
	contract "github.com/morehao/trendradar/contract"
	"github.com/morehao/trendradar/internal/releasewatch"
)

// Stage is one timed step of a run.
type Stage struct {
	Name       string `json:"name"`
	DurationMs int64  `json:"durationMs"`
	Degraded   bool   `json:"degraded,omitempty"`
	Retries    int    `json:"retries,omitempty"`
}

// EnrichReport is the enrich-stage diagnostic. It mirrors enrich.Report but lives
// in app so the wire/report layer does not depend on the enrich package. README
// text and credentials are never included.
type EnrichReport struct {
	Mode        string `json:"mode,omitempty"`
	Requested   int    `json:"requested"`
	Hit         int    `json:"hit"`
	Fetched     int    `json:"fetched"`
	Failed      int    `json:"failed"`
	RateLimited int    `json:"rateLimited"`
	Skipped     int    `json:"skipped,omitempty"`
}

// Report summarizes a run's timing and warnings. Usage is the shared contract
// type so the core and the TypeScript plugin agree on the wire shape.
type Report struct {
	Stages          []Stage         `json:"stages"`
	Warnings        []string        `json:"warnings,omitempty"`
	TotalMs         int64           `json:"totalMs"`
	Usage           *contract.Usage `json:"usage,omitempty"`
	Enrich          *EnrichReport   `json:"enrich,omitempty"`
	Releases        *ReleaseReport  `json:"releases,omitempty"`
	SummaryDegraded bool            `json:"summaryDegraded,omitempty"`
}

// ReleaseReport is the wire shape of the releases-stage diagnostic. The shape
// lives in the contract (schema/releases.v2.json $defs/ReleasesReport); this
// alias keeps the core and the plugin in lockstep. Token cost is carried in
// Usage, which is always serialized, so a consumer can distinguish "nothing
// spent" (calls=0) from "spent but the provider reported nothing"
// (calls>0 with usageKnown=false).
type ReleaseReport = contract.ReleasesReport

func fromReleaseWatch(r releasewatch.Report) *ReleaseReport {
	return &ReleaseReport{
		Enabled:          r.Enabled,
		Repos:            r.Repos,
		NewReleases:      r.NewReleases,
		Summarized:       intPtrIfNonZero(r.Summarized),
		Degraded:         intPtrIfNonZero(r.Degraded),
		Failed:           intPtrIfNonZero(r.Failed),
		RateLimited:      intPtrIfNonZero(r.RateLimited),
		Skipped:          intPtrIfNonZero(r.Skipped),
		Truncated:        intPtrIfNonZero(r.Truncated),
		HistoryTruncated: intPtrIfNonZero(r.HistoryTruncated),
		DurationMs:       int(r.DurationMs),
		Usage: contract.Usage{
			PromptTokens:     r.PromptTokens,
			CompletionTokens: r.CompletionTokens,
			TotalTokens:      r.TotalTokens,
			UsageKnown:       r.UsageKnown,
			Calls:            r.Calls,
		},
	}
}

// intPtrIfNonZero keeps optional counters out of the JSON when zero, matching
// the schema's non-required counters. Usage fields are NOT treated this way:
// they are always emitted so zero cost stays explicit.
func intPtrIfNonZero(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}
