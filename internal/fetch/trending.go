package fetch

import (
	"context"
	"net/url"
	"sync"

	"golang.org/x/sync/errgroup"
)

// FetchTrending downloads the trending HTML for each language concurrently and
// returns a map keyed by language ("all" means the global page).
func (c *Client) FetchTrending(ctx context.Context, languages []string, since string) (map[string][]byte, error) {
	g, gctx := errgroup.WithContext(ctx)
	var mu sync.Mutex
	out := make(map[string][]byte, len(languages))

	for _, lang := range languages {
		lang := lang
		g.Go(func() error {
			body, err := c.getHTML(gctx, trendingPath(lang, since))
			if err != nil {
				return err
			}
			mu.Lock()
			out[lang] = body
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
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
