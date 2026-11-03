package fetch

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/ipc"
)

// searchFallbackPerLang caps how many repos each language contributes to the
// search fallback. It is an internal constant on purpose: the search API has no
// "trending per language" concept, so a tunable knob here would be a setting
// that does not behave like the trending path (rootfix plan §7).
const searchFallbackPerLang = 30

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

type langRepos struct {
	lang  string
	repos []digest.RepoSnapshot
}

// SearchFallback queries the search API per language when HTML scraping is
// unavailable. The search API does not expose per-period star deltas, so every
// repo's StarsInPeriod is 0 and the caller must label the digest as degraded.
func (c *Client) SearchFallback(ctx context.Context, languages []string, since string) ([]digest.RepoSnapshot, error) {
	return c.SearchFallbackAt(ctx, time.Now().UTC(), languages, since)
}

// SearchFallbackAt is the deterministic variant used by tests.
//
// It issues one search per real language (skipping "all"), querying repos
// pushed since the previous day ordered by stars, so the fallback result keeps
// the per-language shape of the trending path. When no real language is given
// (only "all"/empty), it issues a single unfiltered query.
func (c *Client) SearchFallbackAt(ctx context.Context, now time.Time, languages []string, since string) ([]digest.RepoSnapshot, error) {
	langs := normalizeFallbackLangs(languages)

	g, gctx := errgroup.WithContext(ctx)
	var mu sync.Mutex
	var collected []langRepos

	for _, lang := range langs {
		lang := lang
		g.Go(func() error {
			repos, err := c.searchOne(gctx, now, lang)
			if err != nil {
				return err
			}
			mu.Lock()
			collected = append(collected, langRepos{lang: lang, repos: repos})
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return mergeFallback(collected), nil
}

// normalizeFallbackLangs dedups and drops the pseudo-language "all"; when
// nothing remains it returns a single empty language (unfiltered query).
func normalizeFallbackLangs(languages []string) []string {
	seen := make(map[string]bool, len(languages))
	out := make([]string, 0, len(languages))
	for _, l := range languages {
		l = strings.ToLower(strings.TrimSpace(l))
		if l == "" || l == "all" || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// searchOne runs a single per-language search query.
func (c *Client) searchOne(ctx context.Context, now time.Time, lang string) ([]digest.RepoSnapshot, error) {
	// pushed:>yesterday keeps the query to recently active repos; sort=stars
	// surfaces the most popular. This is a trend approximation, not a delta.
	q := "pushed:>" + now.AddDate(0, 0, -1).Format("2006-01-02")
	if lang != "" {
		q += " language:" + lang
	}
	path := "/search/repositories?q=" + url.QueryEscape(q) +
		"&sort=stars&order=desc&per_page=" + strconv.Itoa(searchFallbackPerLang)

	body, err := c.getAPI(ctx, path)
	if err != nil {
		return nil, err
	}
	var sr searchResponse
	if err := json.Unmarshal(body, &sr); err != nil {
		return nil, &Error{Code: ipc.CodeFetchFailed, Message: "decode search response", Err: err}
	}

	out := make([]digest.RepoSnapshot, 0, len(sr.Items))
	for _, it := range sr.Items {
		l := strings.ToLower(strings.TrimSpace(it.Language))
		if l == "" {
			l = "unknown"
		}
		out = append(out, digest.RepoSnapshot{
			FullName:      it.FullName,
			URL:           it.HTMLURL,
			Description:   digest.StrPtr(it.Description),
			Language:      l,
			Stars:         it.Stars,
			StarsInPeriod: 0,
			Forks:         digest.IntPtr(it.Forks),
			Source:        digest.SourceSearchAPI,
		})
	}
	return out, nil
}

// mergeFallback flattens per-language groups in a stable (language) order,
// de-duplicating repos that appear under more than one language, and assigns
// global ranks.
func mergeFallback(collected []langRepos) []digest.RepoSnapshot {
	sort.SliceStable(collected, func(i, j int) bool { return collected[i].lang < collected[j].lang })

	seen := make(map[string]bool)
	var out []digest.RepoSnapshot
	for _, grp := range collected {
		for _, r := range grp.repos {
			if seen[r.FullName] {
				continue
			}
			seen[r.FullName] = true
			r.Rank = len(out) + 1
			out = append(out, r)
		}
	}
	return out
}
