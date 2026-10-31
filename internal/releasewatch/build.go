package releasewatch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/morehao/trendradar/internal/fetch"
	"github.com/morehao/trendradar/internal/ipc"
	"github.com/morehao/trendradar/internal/model"
	"github.com/morehao/trendradar/internal/releasedigest"
	"github.com/morehao/trendradar/internal/summarize"
)

const defaultPageSize = 30

// Fetcher is the subset of fetch.Client used here, so tests can fake it.
type Fetcher interface {
	ListReleasesPage(ctx context.Context, owner, repo string, page, pageSize int) (fetch.ReleasePage, error)
	ListTagsPage(ctx context.Context, owner, repo string, page, pageSize int) (fetch.TagPage, error)
	GetCommit(ctx context.Context, owner, repo, sha string) (fetch.Commit, error)
}

// Options configures one release-watch build.
type Options struct {
	Repos              []string
	Date               string
	Period             string
	IncludePrerelease  bool
	IncludeDrafts      bool
	FallbackToTags     bool
	BaselineOnFirstRun bool
	BackfillLatest     int
	PerRepoLimit       int
	MaxNotesChars      int
	MaxPages           int

	Provider     model.Provider
	ProviderName string
	ModelName    string
	StatePath    string
	CachePath    string
	ReadOnly     bool
}

// Report is the per-run diagnostic for the releases stage.
type Report struct {
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

	PromptTokens     int  `json:"promptTokens,omitempty"`
	CompletionTokens int  `json:"completionTokens,omitempty"`
	TotalTokens      int  `json:"totalTokens,omitempty"`
	UsageKnown       bool `json:"usageKnown,omitempty"`
	Calls            int  `json:"calls,omitempty"`
}

type builder struct {
	opts  Options
	fet   Fetcher
	state *State
	cache *summarize.Cache
	sum   *Summarizer
	rep   *Report

	commits map[string]*fetch.Commit
}

// Build fetches new releases for every subscribed repo, summarizes them, and
// returns a ReleaseDigest plus the updated cursor. It never saves the cursor:
// the caller must persist it only after the digest is published, so a failed
// publish does not advance the cursor past unseen releases.
func Build(ctx context.Context, f Fetcher, opts Options) (releasedigest.ReleaseDigest, *State, Report, error) {
	start := time.Now()
	period := opts.Period
	if period == "" {
		period = "daily"
	}
	rep := Report{Enabled: true, Repos: len(opts.Repos)}
	state, err := LoadState(opts.StatePath)
	if err != nil {
		return releasedigest.ReleaseDigest{}, nil, rep, fmt.Errorf("releasewatch: load state: %w", err)
	}
	var cache *summarize.Cache
	if opts.CachePath != "" {
		cache, _ = summarize.LoadCache(opts.CachePath)
	}
	if cache == nil {
		cache = summarize.NewCache()
	}
	cache.SetNamespace(fmt.Sprintf("%s|%s|%s", opts.ProviderName, opts.ModelName, PromptVersion))

	b := &builder{
		opts:  opts,
		fet:   f,
		state: state,
		cache: cache,
		sum:   NewSummarizer(opts.Provider, cache, opts.MaxNotesChars),
		rep:   &rep,
	}

	d := releasedigest.ReleaseDigest{Schema: releasedigest.Schema, Date: opts.Date, Period: period}
	for i, repo := range opts.Repos {
		group, class := b.processRepo(ctx, repo)
		if group != nil {
			d.Repos = append(d.Repos, *group)
		}
		if class == "ratelimited" {
			rep.RateLimited++
			// Stop hitting the API once quota is exhausted; count the rest as skipped.
			rep.Skipped += len(opts.Repos) - i - 1
			break
		}
		if class == "failed" {
			rep.Failed++
		}
	}
	d.Count = d.CountOf()
	d.Degraded = d.DegradedOf()
	d.ContentHashAlgo = releasedigest.ContentHashAlgo
	d.ContentHash = releasedigest.ContentHash(d)
	usage, calls := b.sum.Stats()
	rep.PromptTokens = usage.PromptTokens
	rep.CompletionTokens = usage.CompletionTokens
	rep.TotalTokens = usage.TotalTokens
	rep.UsageKnown = usage.Known
	rep.Calls = calls
	rep.DurationMs = time.Since(start).Milliseconds()

	if !opts.ReadOnly && opts.CachePath != "" {
		_ = cache.Save(opts.CachePath)
	}
	return d, state, rep, nil
}

func (b *builder) processRepo(ctx context.Context, repo string) (*releasedigest.RepoReleases, string) {
	owner, name, ok := splitRepo(repo)
	if !ok {
		return nil, "failed"
	}
	st, hasState := b.state.Get(repo)

	if !hasState && b.opts.BaselineOnFirstRun {
		return b.baseline(ctx, repo, owner, name)
	}

	// Regular path: page newest -> oldest until we cross the cursor.
	var all []fetch.Release
	hitCap := false
	sawReleases := false
	for page := 1; page <= b.opts.MaxPages; page++ {
		pg, err := b.fet.ListReleasesPage(ctx, owner, name, page, defaultPageSize)
		if err != nil {
			return nil, classify(err)
		}
		if len(pg.Releases) > 0 {
			sawReleases = true
		}
		stop := false
		for _, r := range filterReleases(pg.Releases, b.opts) {
			if hasState && r.ID <= st.LastID {
				stop = true
				break
			}
			all = append(all, r)
		}
		if stop || !pg.HasNext {
			break
		}
		if page == b.opts.MaxPages && pg.HasNext {
			hitCap = true
		}
	}
	if hitCap {
		b.rep.HistoryTruncated++
	}

	cands := make([]candidate, 0, len(all))
	for _, r := range all {
		cands = append(cands, candidate{repoFull: repo, repoURL: repoURL(repo), rel: r, kind: "release"})
	}

	// Fall back to tags only for repos that actually have no releases. A repo
	// that uses releases but merely has nothing new must NOT switch to tags,
	// otherwise older tags outside the baseline page reappear as "new".
	tagMode := !sawReleases && !(hasState && st.LastID > 0)
	if len(cands) == 0 && b.opts.FallbackToTags && tagMode {
		tags, capHit := b.fetchNewTags(ctx, owner, name, st.SeenTags)
		if capHit {
			b.rep.HistoryTruncated++
		}
		for _, t := range tags {
			cands = append(cands, b.tagCandidate(ctx, repo, owner, name, t))
		}
	}
	if len(cands) == 0 {
		return nil, ""
	}

	// cands are newest-first. Process the oldest `perRepoLimit` first so the
	// cursor never jumps over unprocessed versions.
	reverse(cands)
	if len(cands) > b.opts.PerRepoLimit {
		b.rep.Truncated++
		cands = cands[:b.opts.PerRepoLimit]
	}
	// Advance cursor to the newest item in this batch (last after oldest-first sort).
	next := st
	for _, c := range cands {
		if c.rel.ID > 0 {
			next.LastID = c.rel.ID
		}
		if c.rel.PublishedAt != nil {
			next.LastPublishedAt = c.rel.PublishedAt
		}
		next.LastTag = c.tagName()
		AddSeenTag(&next, c.tagName())
	}
	b.state.Set(repo, next)

	// Render newest-first.
	reverse(cands)
	return b.summarizeGroup(ctx, repo, cands), ""
}

func (b *builder) baseline(ctx context.Context, repo, owner, name string) (*releasedigest.RepoReleases, string) {
	page, err := b.fet.ListReleasesPage(ctx, owner, name, 1, defaultPageSize)
	if err != nil {
		return nil, classify(err)
	}
	rels := filterReleases(page.Releases, b.opts)
	st := RepoState{}
	var pool []candidate
	if len(rels) > 0 {
		latest := rels[0]
		st.LastID = latest.ID
		st.LastTag = latest.TagName
		st.LastPublishedAt = latest.PublishedAt
		for _, r := range rels {
			AddSeenTag(&st, r.TagName)
			pool = append(pool, candidate{repoFull: repo, repoURL: repoURL(repo), rel: r, kind: "release"})
		}
	} else if b.opts.FallbackToTags {
		tags, _ := b.fetchAllTags(ctx, owner, name)
		if len(tags) == 0 {
			return nil, ""
		}
		st.LastTag = tags[0].Name
		for _, t := range tags {
			AddSeenTag(&st, t.Name)
			pool = append(pool, b.tagCandidate(ctx, repo, owner, name, t))
		}
	} else {
		return nil, ""
	}
	b.state.Set(repo, st)
	if b.opts.BackfillLatest <= 0 || len(pool) == 0 {
		return nil, ""
	}
	n := b.opts.BackfillLatest
	if n > len(pool) {
		n = len(pool)
	}
	return b.summarizeGroup(ctx, repo, pool[:n]), ""
}

func (b *builder) summarizeGroup(ctx context.Context, repo string, cands []candidate) *releasedigest.RepoReleases {
	results := b.sum.Summarize(ctx, cands)
	items := make([]releasedigest.ReleaseItem, 0, len(cands))
	for _, c := range cands {
		r := results[c.repoFull+"\x00"+c.tagName()]
		item := releasedigest.ReleaseItem{
			ID:            c.rel.ID,
			Tag:           c.tagName(),
			Name:          c.rel.Name,
			Kind:          c.kind,
			PublishedAt:   c.rel.PublishedAt,
			URL:           c.rel.HTMLURL,
			Prerelease:    c.rel.Prerelease,
			Draft:         c.rel.Draft,
			SummaryZh:     r.SummaryZh,
			SummarySource: r.Source,
		}
		if item.Name == "" {
			item.Name = item.Tag
		}
		if item.URL == "" {
			item.URL = repoURL(repo)
		}
		if c.rel.Body != "" {
			item.BodyExcerpt = truncateRunes(c.rel.Body, b.opts.MaxNotesChars)
		}
		b.rep.NewReleases++
		if r.Source == "model" {
			b.rep.Summarized++
		} else {
			b.rep.Degraded++
		}
		items = append(items, item)
	}
	return &releasedigest.RepoReleases{
		FullName:  repo,
		URL:       repoURL(repo),
		LatestTag: items[0].Tag,
		NewCount:  len(items),
		Releases:  items,
	}
}

// tagCandidate builds a tag entry and, when the repo has no release notes, uses
// the tagged commit's message as the summary basis so the digest is not just
// "no changelog". Commit lookup failures degrade silently.
func (b *builder) tagCandidate(ctx context.Context, repo, owner, name string, t fetch.Tag) candidate {
	rel := fetch.Release{
		TagName:     t.Name,
		Name:        t.Name,
		HTMLURL:     repoURL(repo) + "/tree/" + url.PathEscape(t.Name),
		PublishedAt: nil,
	}
	b.getCommitOnce(owner, name, t.Commit.SHA, &rel)
	return candidate{repoFull: repo, repoURL: repoURL(repo), rel: rel, kind: "tag"}
}

// commitCache memoizes commit lookups per repo+sha so overlapping tag lists
// (new tags plus baseline) do not double-spend GitHub quota. Failures are
// memoized too: a missing commit degrades to "no changelog", never a retry loop.
func (b *builder) getCommitOnce(owner, name, sha string, rel *fetch.Release) {
	if sha == "" {
		return
	}
	key := owner + "/" + name + "@" + sha
	if b.commits == nil {
		b.commits = map[string]*fetch.Commit{}
	}
	if cm, ok := b.commits[key]; ok {
		applyCommit(rel, sha, cm)
		return
	}
	cm, err := b.fet.GetCommit(context.Background(), owner, name, sha)
	if err != nil {
		b.commits[key] = nil
		return
	}
	b.commits[key] = &cm
	applyCommit(rel, sha, &cm)
}

func applyCommit(rel *fetch.Release, sha string, cm *fetch.Commit) {
	if cm == nil {
		return
	}
	if msg := strings.TrimSpace(cm.Commit.Message); msg != "" {
		rel.Body = "commit " + shortSHA(sha) + ": " + msg
	}
	if cm.Commit.Author.Date != nil {
		rel.PublishedAt = cm.Commit.Author.Date
	}
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func (b *builder) fetchNewTags(ctx context.Context, owner, name string, seen []string) ([]fetch.Tag, bool) {
	var out []fetch.Tag
	for page := 1; page <= b.opts.MaxPages; page++ {
		pg, err := b.fet.ListTagsPage(ctx, owner, name, page, defaultPageSize)
		if err != nil {
			return out, false
		}
		for _, t := range pg.Tags {
			if !contains(seen, t.Name) {
				out = append(out, t)
			}
		}
		if !pg.HasNext {
			return out, false
		}
		if page == b.opts.MaxPages && pg.HasNext {
			return out, true
		}
	}
	return out, false
}

func (b *builder) fetchAllTags(ctx context.Context, owner, name string) ([]fetch.Tag, bool) {
	var out []fetch.Tag
	for page := 1; page <= b.opts.MaxPages; page++ {
		pg, err := b.fet.ListTagsPage(ctx, owner, name, page, defaultPageSize)
		if err != nil {
			return out, false
		}
		out = append(out, pg.Tags...)
		if !pg.HasNext {
			return out, false
		}
		if page == b.opts.MaxPages && pg.HasNext {
			return out, true
		}
	}
	return out, false
}

func filterReleases(in []fetch.Release, opts Options) []fetch.Release {
	out := make([]fetch.Release, 0, len(in))
	for _, r := range in {
		if r.Draft && !opts.IncludeDrafts {
			continue
		}
		if r.Prerelease && !opts.IncludePrerelease {
			continue
		}
		out = append(out, r)
	}
	return out
}

func classify(err error) string {
	var fe *fetch.Error
	if errors.As(err, &fe) && fe.Code == ipc.CodeFetchRateLimited {
		return "ratelimited"
	}
	return "failed"
}

func splitRepo(repo string) (owner, name string, ok bool) {
	parts := strings.Split(strings.TrimSpace(repo), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func repoURL(repo string) string { return "https://github.com/" + repo }

func reverse[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
