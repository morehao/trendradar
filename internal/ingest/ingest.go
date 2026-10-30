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

// Result is what one ingest run produced.
type Result struct {
	Repos    []digest.RepoSnapshot
	Report   digest.ParseReport
	Degraded bool // true when the search API fallback was used
}

// Ingest fetches trending pages, parses them, and falls back to the search API
// when scraping fails or yields no rows.
func Ingest(ctx context.Context, c *fetch.Client, languages []string, since string, perLang int) (Result, error) {
	pages, err := c.FetchTrending(ctx, languages, since)
	if err != nil {
		return fallback(ctx, c, since, perLang)
	}
	repos, report, perr := parse.ParseAll(pages)
	if perr != nil || len(repos) == 0 {
		return fallback(ctx, c, since, perLang)
	}
	return Result{Repos: repos, Report: report}, nil
}

func fallback(ctx context.Context, c *fetch.Client, since string, perLang int) (Result, error) {
	repos, err := c.SearchFallback(ctx, since, perLang)
	if err != nil {
		return Result{}, &fetch.Error{
			Code:    ipc.CodeFetchFailed,
			Message: "trending HTML and search API both failed",
			Err:     err,
		}
	}
	return Result{
		Repos:    repos,
		Report:   digest.ParseReport{Total: len(repos)},
		Degraded: true,
	}, nil
}
