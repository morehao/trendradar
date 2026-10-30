package fetch

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/ipc"
)

type searchResponse struct {
	Items []struct {
		FullName    string `json:"full_name"`
		HTMLURL     string `json:"html_url"`
		Description string `json:"description"`
		Language    string `json:"language"`
		Stars       int    `json:"stargazers_count"`
		Forks       int    `json:"forks_count"`
	} `json:"items"`
}

// SearchFallback queries the search API for recently created, high-star repos.
// It is used when HTML scraping is unavailable; starsInPeriod is unknown (0)
// because the search API does not expose per-period deltas.
func (c *Client) SearchFallback(ctx context.Context, since string, limit int) ([]digest.RepoSnapshot, error) {
	return c.SearchFallbackAt(ctx, time.Now().UTC(), since, limit)
}

// SearchFallbackAt is the deterministic variant used by tests.
func (c *Client) SearchFallbackAt(ctx context.Context, now time.Time, since string, limit int) ([]digest.RepoSnapshot, error) {
	if limit <= 0 {
		limit = 20
	}
	q := "created:>" + now.AddDate(0, 0, -7).Format("2006-01-02")
	path := "/search/repositories?q=" + url.QueryEscape(q) +
		"&sort=stars&order=desc&per_page=" + strconv.Itoa(limit)

	body, err := c.getAPI(ctx, path)
	if err != nil {
		return nil, err
	}
	var sr searchResponse
	if err := json.Unmarshal(body, &sr); err != nil {
		return nil, &Error{Code: ipc.CodeFetchFailed, Message: "decode search response", Err: err}
	}

	out := make([]digest.RepoSnapshot, 0, len(sr.Items))
	for i, it := range sr.Items {
		lang := strings.ToLower(strings.TrimSpace(it.Language))
		if lang == "" {
			lang = "unknown"
		}
		out = append(out, digest.RepoSnapshot{
			FullName:      it.FullName,
			URL:           it.HTMLURL,
			Description:   it.Description,
			Language:      lang,
			Stars:         it.Stars,
			StarsInPeriod: 0,
			Forks:         it.Forks,
			Rank:          i + 1,
			Source:        digest.SourceSearchAPI,
		})
	}
	return out, nil
}
