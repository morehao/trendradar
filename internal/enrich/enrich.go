// Package enrich fetches optional external context (GitHub topics and a README
// excerpt) for repositories before summarization. It is best-effort by design:
// a per-repository failure is recorded in the report and never fails the run.
//
// GitHub traffic avoids the REST 60/h limit by reading the repository HTML for
// topics + default branch and raw.githubusercontent.com for the README. Both are
// cached for a week and only fetched for repos whose summary is not cached.
package enrich

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/morehao/trendradar/internal/digest"
)

// Mode selects how much external context to fetch. The zero value is none.
type Mode string

const (
	// ModeNone disables external enrichment (the default).
	ModeNone Mode = "none"
	// ModeRepo fetches topics (and default branch) only.
	ModeRepo Mode = "repo"
	// ModeReadme fetches topics plus a truncated README excerpt.
	ModeReadme Mode = "readme"
)

// ParseMode validates a configured mode string. Empty means none.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(ModeNone):
		return ModeNone, nil
	case string(ModeRepo):
		return ModeRepo, nil
	case string(ModeReadme):
		return ModeReadme, nil
	default:
		return ModeNone, fmt.Errorf("enrich: invalid mode %q (want none|repo|readme)", s)
	}
}

// Valid reports whether m is a known mode.
func (m Mode) Valid() bool { return m == ModeNone || m == ModeRepo || m == ModeReadme }

// String implements fmt.Stringer.
func (m Mode) String() string { return string(m) }

// Context is the external context for one repository. It is internal to the
// core and never written to the digest wire.
type Context struct {
	Topics        []string
	ReadmeExcerpt string
}

// Report is a per-run diagnostic of the enrich stage. It never contains README
// text or credentials.
type Report struct {
	Mode        string `json:"mode,omitempty"`
	Requested   int    `json:"requested"`
	Hit         int    `json:"hit"`         // served from the enrich cache
	Fetched     int    `json:"fetched"`     // freshly fetched (topics and/or readme)
	Failed      int    `json:"failed"`      // per-repo failure, degraded to no context
	RateLimited int    `json:"rateLimited"` // repos skipped after a 429/403 quota signal
	Skipped     int    `json:"skipped"`     // repos skipped (budget exhausted or read-only miss)
}

// Options configures an Enricher. Zero values pick sensible defaults.
type Options struct {
	Mode        Mode
	ReadmeChars int           // README excerpt length in runes (default 800)
	Concurrency int           // concurrent repo fetches (default 4)
	Timeout     time.Duration // per-request HTTP timeout (default 5s)
	Budget      time.Duration // whole-run enrich budget (default 8s; readme 12s)
	Token       string        // optional read-only GitHub token
	HTMLBase    string        // default https://github.com
	RawBase     string        // default https://raw.githubusercontent.com
	CachePath   string        // persisted cache path under <repo>/.git
	ReadOnly    bool          // only read the cache; never hit the network
	Client      *http.Client  // optional HTTP client override (tests)
}

// Enricher fetches and caches external context. It is safe for concurrent use
// across many goroutines within one Enrich call.
type Enricher struct {
	mode        Mode
	readmeChars int
	concurrency int
	budget      time.Duration
	token       string
	htmlBase    string
	rawBase     string
	client      *http.Client
	cache       *Cache
	cachePath   string
	readOnly    bool
	stopped     atomic.Bool
}

const (
	maxHTMLBytes      = 2 << 20 // 2 MiB
	maxReadmeBytes    = 512 << 10
	maxReadmeAttempts = 8
)

// reFullName guards the "owner/repo" shape before it is placed in a URL.
var reFullName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

var (
	errRateLimited = errors.New("enrich: rate limited")
	errNotFound    = errors.New("enrich: not found")
	errBudget      = errors.New("enrich: budget exhausted")
)

// New builds an Enricher, applying defaults and attaching cache (nil -> empty).
func New(opts Options, cache *Cache) *Enricher {
	mode := opts.Mode
	if !mode.Valid() {
		mode = ModeNone
	}
	if cache == nil {
		cache = NewCache()
	}
	readmeChars := opts.ReadmeChars
	if readmeChars <= 0 {
		readmeChars = 800
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	budget := opts.Budget
	if budget <= 0 {
		if mode == ModeReadme {
			budget = 12 * time.Second
		} else {
			budget = 8 * time.Second
		}
	}
	htmlBase := strings.TrimRight(opts.HTMLBase, "/")
	if htmlBase == "" {
		htmlBase = "https://github.com"
	}
	rawBase := strings.TrimRight(opts.RawBase, "/")
	if rawBase == "" {
		rawBase = "https://raw.githubusercontent.com"
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return errors.New("enrich: too many redirects")
				}
				// Never follow a redirect to a different host: a 301 to another
				// repo/private location must not read data with our token.
				if req.URL.Host != via[0].URL.Host {
					return fmt.Errorf("enrich: refusing cross-host redirect to %q", req.URL.Host)
				}
				return nil
			},
		}
	}
	return &Enricher{
		mode:        mode,
		readmeChars: readmeChars,
		concurrency: concurrency,
		budget:      budget,
		token:       opts.Token,
		htmlBase:    htmlBase,
		rawBase:     rawBase,
		client:      client,
		cache:       cache,
		cachePath:   opts.CachePath,
		readOnly:    opts.ReadOnly,
	}
}

// Mode returns the configured enrichment mode.
func (e *Enricher) Mode() Mode { return e.mode }

// SaveCache persists the enrich cache if a path was configured.
func (e *Enricher) SaveCache() error {
	if e.cache == nil || e.cachePath == "" {
		return nil
	}
	return e.cache.Save(e.cachePath)
}

// Enrich fetches context for repos, returning a map keyed by full name and a
// report. Cache hits never touch the network. Any single-repo error is recorded
// in the report; Enrich itself returns no error.
func (e *Enricher) Enrich(ctx context.Context, repos []digest.RepoSnapshot) (map[string]Context, Report) {
	rep := Report{Mode: string(e.mode), Requested: len(repos)}
	out := make(map[string]Context, len(repos))
	if e.mode == ModeNone || len(repos) == 0 {
		return out, rep
	}

	todo := make([]digest.RepoSnapshot, 0, len(repos))
	for _, r := range repos {
		if c, ok := e.cache.get(r.FullName, e.mode); ok {
			out[r.FullName] = c
			rep.Hit++
			continue
		}
		todo = append(todo, r)
	}
	if len(todo) == 0 {
		return out, rep
	}
	if e.readOnly {
		rep.Skipped += len(todo)
		return out, rep
	}

	bctx, cancel := context.WithTimeout(ctx, e.budget)
	defer cancel()

	sem := make(chan struct{}, e.concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, r := range todo {
		wg.Add(1)
		go func(r digest.RepoSnapshot) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-bctx.Done():
				mu.Lock()
				rep.Skipped++
				mu.Unlock()
				return
			}
			defer func() { <-sem }()

			if e.stopped.Load() {
				mu.Lock()
				rep.RateLimited++
				mu.Unlock()
				return
			}

			c, err := e.enrichOne(bctx, r.FullName)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				e.cache.put(r.FullName, e.mode, c)
				out[r.FullName] = c
				rep.Fetched++
			case errors.Is(err, errRateLimited):
				e.stopped.Store(true)
				rep.RateLimited++
			case errors.Is(err, errBudget):
				rep.Skipped++
			default:
				rep.Failed++
			}
		}(r)
	}
	wg.Wait()
	return out, rep
}

// enrichOne fetches context for one repository. In readme mode a topics failure
// does not block the README fetch (and vice versa): partial context is still
// useful. Rate-limit and budget signals are fatal for the current repo and
// propagate to stop new work.
func (e *Enricher) enrichOne(ctx context.Context, fullName string) (Context, error) {
	if !reFullName.MatchString(fullName) {
		return Context{}, fmt.Errorf("enrich: invalid repo name %q", fullName)
	}

	var c Context
	body, htmlErr := e.fetch(ctx, e.htmlBase+"/"+fullName, maxHTMLBytes)
	if htmlErr == nil {
		c.Topics = parseTopics(body)
		if e.mode == ModeReadme {
			if ex, rerr := e.fetchReadme(ctx, fullName, parseDefaultBranch(body)); rerr != nil {
				if isFatal(rerr) {
					return c, rerr
				}
				// README missing but topics present: keep topics, not a failure.
			} else {
				c.ReadmeExcerpt = ex
			}
		}
		return c, nil
	}
	if isFatal(htmlErr) {
		return Context{}, htmlErr
	}
	if e.mode != ModeReadme {
		return Context{}, htmlErr
	}
	// HTML failed but readme mode can still try the raw endpoint directly.
	ex, rerr := e.fetchReadme(ctx, fullName, "")
	if rerr != nil {
		if isFatal(rerr) {
			return c, rerr
		}
		return Context{}, rerr
	}
	c.ReadmeExcerpt = ex
	return c, nil
}

// fetchReadme tries README variants against the default branch, HEAD, main and
// master, stopping at the first hit or on a fatal (rate/budget) error.
func (e *Enricher) fetchReadme(ctx context.Context, fullName, branch string) (string, error) {
	branches := uniqueNonEmpty(branch, "HEAD", "main", "master")
	attempts := 0
	var lastErr error = errNotFound
	for _, br := range branches {
		for _, name := range readmeNames {
			if attempts >= maxReadmeAttempts {
				return "", lastErr
			}
			attempts++
			u := e.rawBase + "/" + fullName + "/" + url.PathEscape(br) + "/" + name
			body, err := e.fetch(ctx, u, maxReadmeBytes)
			if err != nil {
				lastErr = err
				if isFatal(err) {
					return "", err
				}
				continue
			}
			ex := truncateRunes(cleanReadme(string(body)), e.readmeChars)
			if ex == "" {
				lastErr = errNotFound
				continue
			}
			return ex, nil
		}
	}
	return "", lastErr
}

// fetch performs one GET and classifies the response. It caps the body size and
// maps 404/429/403-quota to sentinels so callers can degrade correctly.
func (e *Enricher) fetch(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "trendradar-enrich")
	req.Header.Set("Accept", "text/html,text/plain;q=0.9,*/*;q=0.8")
	if e.token != "" {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errBudget
		}
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, errNotFound
	case http.StatusTooManyRequests:
		return nil, errRateLimited
	case http.StatusForbidden:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return nil, errRateLimited
		}
		return nil, fmt.Errorf("enrich: status 403")
	default:
		return nil, fmt.Errorf("enrich: status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		if ctx.Err() != nil {
			return nil, errBudget
		}
		return nil, err
	}
	return data, nil
}

func isFatal(err error) bool {
	return errors.Is(err, errRateLimited) || errors.Is(err, errBudget)
}

func uniqueNonEmpty(vals ...string) []string {
	seen := make(map[string]bool, len(vals))
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
