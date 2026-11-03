package fetch

import (
	"context"
	"net/url"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/morehao/trendradar/internal/ipc"
)

// TrendingResult is the outcome of fetching trending pages. Pages holds the
// successfully downloaded pages keyed by language ("all" means the global
// page); FailedLangs lists the languages whose fetch failed. A partial result
// (some pages, some failures) is returned without error so one flaky language
// never discards the rest — callers decide how to treat FailedLangs.
type TrendingResult struct {
	Pages       map[string][]byte
	FailedLangs []string
}

// Any reports whether at least one page was fetched successfully.
func (r TrendingResult) Any() bool { return len(r.Pages) > 0 }

// FetchTrending downloads the trending HTML for each language concurrently.
// It tolerates per-language failures: successful pages are returned alongside
// the list of languages that failed. It returns an error only when every
// language failed, so callers can fall back to the search API in that case.
func (c *Client) FetchTrending(ctx context.Context, languages []string, since string) (TrendingResult, error) {
	g, gctx := errgroup.WithContext(ctx)
	var mu sync.Mutex
	out := make(map[string][]byte, len(languages))
	var failed []string

	for _, lang := range languages {
		lang := lang
		g.Go(func() error {
			body, err := c.getHTML(gctx, trendingPath(lang, since))
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = append(failed, lang)
				return nil // tolerate: do not abort the group
			}
			out[lang] = body
			return nil
		})
	}
	// Every goroutine returns nil, so Wait only surfaces context cancellation.
	if err := g.Wait(); err != nil {
		return TrendingResult{}, err
	}

	res := TrendingResult{Pages: out, FailedLangs: failed}
	if len(res.Pages) == 0 {
		return res, &Error{Code: ipc.CodeFetchFailed, Message: "all languages failed to fetch"}
	}
	return res, nil
}

// trendingPath builds the request path. "all" (or empty) uses the global page.
func trendingPath(lang, since string) string {
	p := "/trending"
	if lang != "" && lang != "all" {
		p += "/" + url.PathEscape(lang)
	}
	if since != "" {
		p += "?since=" + url.QueryEscape(since)
	}
	return p
}
