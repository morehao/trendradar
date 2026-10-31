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

// ReleaseReport mirrors releasewatch.Report so the wire/report layer does not
// depend on that package's types.
type ReleaseReport struct {
	Enabled          bool  `json:"enabled"`
	Repos            int   `json:"repos"`
	NewReleases      int   `json:"newReleases"`
	Summarized       int   `json:"summarized"`
	Degraded         int   `json:"degraded"`
	Failed           int   `json:"failed"`
	RateLimited      int   `json:"rateLimited"`
	Skipped          int   `json:"skipped"`
	Truncated        int   `json:"truncated"`
	HistoryTruncated int   `json:"historyTruncated"`
	DurationMs       int64 `json:"durationMs"`
	PromptTokens     int   `json:"promptTokens,omitempty"`
	CompletionTokens int   `json:"completionTokens,omitempty"`
	TotalTokens      int   `json:"totalTokens,omitempty"`
	UsageKnown       bool  `json:"usageKnown,omitempty"`
	Calls            int   `json:"calls,omitempty"`
}

func fromReleaseWatch(r releasewatch.Report) *ReleaseReport {
	return &ReleaseReport{
		Enabled: r.Enabled, Repos: r.Repos, NewReleases: r.NewReleases,
		Summarized: r.Summarized, Degraded: r.Degraded, Failed: r.Failed,
		RateLimited: r.RateLimited, Skipped: r.Skipped, Truncated: r.Truncated,
		HistoryTruncated: r.HistoryTruncated, DurationMs: r.DurationMs,
		PromptTokens: r.PromptTokens, CompletionTokens: r.CompletionTokens,
		TotalTokens: r.TotalTokens, UsageKnown: r.UsageKnown, Calls: r.Calls,
	}
}
