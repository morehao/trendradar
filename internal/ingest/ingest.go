// Package ingest wires fetch and parse into one operation with the HTML->search
// fallback described in the plan (M2 T2.9).
package ingest

import (
	"context"

	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/fetch"
	"github.com/morehao/trendradar/internal/ipc"
	"github.com/morehao/trendradar/internal/parse"
)

// Degraded reason codes surfaced on the digest and in warnings.
const (
	// ReasonNone means a clean trending-HTML run.
	ReasonNone = ""
	// ReasonHTMLPartial means some languages failed to fetch/parse but the rest
	// produced trending-HTML rows (per-language tolerance).
	ReasonHTMLPartial = "html_partial"
	// ReasonSearchFallback means the whole HTML path failed and the search API
	// provided the data (no per-period star deltas).
	ReasonSearchFallback = "search_fallback"
)

// Result is what one ingest run produced.
type Result struct {
	Repos    []digest.RepoSnapshot
	Report   digest.ParseReport
	Degraded bool // true when the data is not a clean trending-HTML result
	// DegradedReason explains why the run degraded (one of the Reason* codes).
	// Empty when Degraded is false.
	DegradedReason string
	// DegradedLangs lists languages that failed to fetch or produced no rows and
	// were therefore dropped from this run. Empty for a clean run.
	DegradedLangs []string
}

// Ingest fetches trending pages, parses them, and falls back to the search API
// when scraping fails or yields no rows. Per-language failures are tolerated:
// as long as one language yields rows, that partial result is used (with
// Degraded=true, reason html_partial) instead of discarding everything.
func Ingest(ctx context.Context, c *fetch.Client, languages []string, since string) (Result, error) {
	fr, err := c.FetchTrending(ctx, languages, since)
	if err != nil || !fr.Any() {
		// Every language failed: fall back to the search API.
		return fallback(ctx, c, languages, since)
	}

	repos, report, perr := parse.ParseAll(fr.Pages)
	if perr != nil || len(repos) == 0 {
		return fallback(ctx, c, languages, since)
	}

	// Languages that were requested but contributed no page are degraded even
	// if the pages we did get parsed fine.
	degradedLangs := append([]string(nil), fr.FailedLangs...)
	res := Result{Repos: repos, Report: report}
	if len(degradedLangs) > 0 {
		res.Degraded = true
		res.DegradedReason = ReasonHTMLPartial
		res.DegradedLangs = degradedLangs
	}
	return res, nil
}

func fallback(ctx context.Context, c *fetch.Client, languages []string, since string) (Result, error) {
	repos, err := c.SearchFallback(ctx, languages, since)
	if err != nil {
		return Result{}, &fetch.Error{
			Code:    ipc.CodeFetchFailed,
			Message: "trending HTML and search API both failed",
			Err:     err,
		}
	}
	return Result{
		Repos:          repos,
		Report:         digest.ParseReport{Total: len(repos), Degraded: true},
		Degraded:       true,
		DegradedReason: ReasonSearchFallback,
	}, nil
}
