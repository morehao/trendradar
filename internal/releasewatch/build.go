package releasewatch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
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
	Date               string // run date; the digest is dated by its week end
	Period             string // summary period; weekly is the only supported value
	Timezone           string // IANA name; empty means Asia/Shanghai
	WeekStart          string // only "monday" (ISO 8601) is supported
	IncludePrerelease  bool
	IncludeDrafts      bool
	FallbackToTags     bool
	BaselineOnFirstRun bool
	BackfillLatest     int
	MaxNotesChars      int
	SummaryMaxChars    int
	MaxPages           int
	Concurrency        int
	// BudgetSeconds caps the whole stage. 0 disables the budget. The caller
	// resolves "auto" to a number so the core never has to guess the scale.
	BudgetSeconds int

	Provider     model.Provider
	ProviderName string
	ModelName    string
	StatePath    string
	CachePath    string
	ReadOnly     bool
}

// Report is the per-run diagnostic for the releases stage. Checked and Changed
// are always serialized (a plugin shows "checked N, changed M"); Skipped counts
// repos that were not checked at all this run (already summarized this week, or
// dropped after a rate limit), which is different from "checked, nothing new".
type Report struct {
	Enabled          bool  `json:"enabled"`
	Repos            int   `json:"repos"`
	Checked          int   `json:"checked"`
	Changed          int   `json:"changed"`
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

// statusAlreadyDone is an outcome-only status: the repo's week was already
// summarized, so this run neither touched GitHub nor counted it as "checked".
const statusAlreadyDone = "already_done"

// repoOutcome is one repo's contribution to the report. Returning counters
// instead of mutating the report keeps the concurrent workers race-free.
type repoOutcome struct {
	group            *releasedigest.RepoReleases
	status           string
	newReleases      int
	summarized       int
	degraded         int
	truncated        int
	historyTruncated int
}

type builder struct {
	opts  Options
	fet   Fetcher
	state *State
	cache *summarize.Cache
	sum   *Summarizer
	week  Week

	mu      sync.Mutex
	commits map[string]*fetch.Commit
}

// Build fetches new releases for every subscribed repo, summarizes each repo's
// week in one model call, and returns a ReleaseDigest plus the updated cursor.
// It never saves the cursor: the caller must persist it only after the document
// is published, so a failed publish does not advance the cursor past unseen
// releases.
func Build(ctx context.Context, f Fetcher, opts Options) (releasedigest.ReleaseDigest, *State, Report, error) {
	start := time.Now()
	if opts.BudgetSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(opts.BudgetSeconds)*time.Second)
		defer cancel()
	}
	period := opts.Period
	if period == "" {
		period = "weekly"
	}
	rep := Report{Enabled: true, Repos: len(opts.Repos)}
	state, err := LoadState(opts.StatePath)
	if err != nil {
		return releasedigest.ReleaseDigest{}, nil, rep, fmt.Errorf("releasewatch: load state: %w", err)
	}
	loc, err := Location(opts.Timezone)
	if err != nil {
		return releasedigest.ReleaseDigest{}, nil, rep, err
	}
	week, err := WeekOf(opts.Date, loc)
	if err != nil {
		return releasedigest.ReleaseDigest{}, nil, rep, err
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
		sum:   NewSummarizer(opts.Provider, cache, opts.MaxNotesChars, opts.SummaryMaxChars),
		week:  week,
	}

	d := releasedigest.ReleaseDigest{Schema: releasedigest.Schema, Date: week.End, Period: period}
	outcomes := b.run(ctx, opts.Repos)
	for _, repo := range opts.Repos {
		o, ok := outcomes[repo]
		if !ok {
			// The worker pool stopped before reaching this repo (rate limit).
			rep.Skipped++
			continue
		}
		switch o.status {
		case statusAlreadyDone:
			rep.Skipped++
			continue
		case StatusRateLimited:
			rep.RateLimited++
		case StatusFailed:
			rep.Failed++
		}
		rep.Checked++
		rep.NewReleases += o.newReleases
		rep.Summarized += o.summarized
		rep.Degraded += o.degraded
		// Truncated is counted once by the summarizer (it knows how many of its
		// own outputs were cut), so the per-repo flag is not added again here.
		rep.HistoryTruncated += o.historyTruncated
		if o.group != nil {
			rep.Changed++
			d.Repos = append(d.Repos, *o.group)
		}
	}

	d.Count = releasedigest.CountOf(d)
	d.Degraded = releasedigest.DegradedOf(d)
	d.ContentHashAlgo = releasedigest.ContentHashAlgo
	d.ContentHash = releasedigest.ContentHash(d)
	usage, calls, truncated := b.sum.Stats()
	rep.PromptTokens = usage.PromptTokens
	rep.CompletionTokens = usage.CompletionTokens
	rep.TotalTokens = usage.TotalTokens
	rep.UsageKnown = usage.Known
	rep.Calls = calls
	rep.Truncated += truncated
	rep.DurationMs = time.Since(start).Milliseconds()

	if !opts.ReadOnly && opts.CachePath != "" {
		_ = cache.Save(opts.CachePath)
	}
	return d, state, rep, nil
}

// run processes repos with a bounded worker pool. On a rate limit it cancels the
// pool instead of burning the remaining budget on requests that will fail; the
// repos that never ran are reported as skipped and keep their old cursor.
func (b *builder) run(ctx context.Context, repos []string) map[string]repoOutcome {
	out := make(map[string]repoOutcome, len(repos))
	if len(repos) == 0 {
		return out
	}
	workers := b.opts.Concurrency
	if workers < 1 {
		workers = 1
	}
	if workers > len(repos) {
		workers = len(repos)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan string)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for repo := range jobs {
				if ctx.Err() != nil {
					// Cancelled by a rate limit: leave the repo unprocessed so it is
					// reported as skipped and keeps its old cursor.
					continue
				}
				o := b.processRepo(ctx, repo)
				mu.Lock()
				out[repo] = o
				mu.Unlock()
				if o.status == StatusRateLimited {
					cancel()
				}
			}
		}()
	}

dispatch:
	for _, repo := range repos {
		select {
		case jobs <- repo:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(jobs)
	wg.Wait()
	return out
}

func (b *builder) processRepo(ctx context.Context, repo string) repoOutcome {
	owner, name, ok := splitRepo(repo)
	if !ok {
		b.stamp(repo, StatusFailed, false)
		return repoOutcome{status: StatusFailed}
	}
	st, hasState := b.getState(repo)

	// Weekly gate: a completed check for this ISO week means "do not ask again".
	// Nothing is fetched, summarized or written, so a duplicate run (schedule +
	// manual) costs nothing and cannot append the week twice (A4).
	if hasState && st.WeekKey == b.week.Key {
		return repoOutcome{status: statusAlreadyDone}
	}

	if !hasState && b.opts.BaselineOnFirstRun {
		return b.baseline(ctx, repo, owner, name)
	}

	// Regular path: page newest -> oldest until we cross the cursor. There is no
	// per-repo cap here: the whole week's versions feed one summary, and only the
	// document's bullet list is capped (see releasedoc.MaxListedTags).
	var all []fetch.Release
	hitCap := false
	sawReleases := false
	for page := 1; page <= b.opts.MaxPages; page++ {
		pg, err := b.fet.ListReleasesPage(ctx, owner, name, page, defaultPageSize)
		if err != nil {
			return b.fail(repo, classify(err))
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
			hitCap = true
		}
		for _, t := range tags {
			cands = append(cands, b.tagCandidate(ctx, repo, owner, name, t))
		}
	}
	if len(cands) == 0 {
		b.stamp(repo, StatusNoChange, true)
		o := repoOutcome{status: StatusNoChange}
		if hitCap {
			o.historyTruncated = 1
		}
		return o
	}

	// cands are newest-first. Render newest-first too, but advance the cursor
	// from the oldest item so a future run can never skip an unprocessed version.
	next := st
	for i := len(cands) - 1; i >= 0; i-- {
		c := cands[i]
		if c.rel.ID > 0 {
			next.LastID = c.rel.ID
		}
		if c.rel.PublishedAt != nil {
			next.LastPublishedAt = c.rel.PublishedAt
		}
		next.LastTag = c.tagName()
		AddSeenTag(&next, c.tagName())
	}
	next.WeekKey = b.week.Key
	next.Status = StatusOK
	next.LatestSummaryAt = b.week.End
	b.putState(repo, next)

	group, summarized, degraded, truncated := b.summarizeRepo(ctx, repo, cands)
	o := repoOutcome{
		group:       group,
		status:      StatusOK,
		newReleases: len(cands),
		summarized:  summarized,
		degraded:    degraded,
		truncated:   truncated,
	}
	if hitCap {
		o.historyTruncated = 1
	}
	return o
}

func (b *builder) baseline(ctx context.Context, repo, owner, name string) repoOutcome {
	page, err := b.fet.ListReleasesPage(ctx, owner, name, 1, defaultPageSize)
	if err != nil {
		return b.fail(repo, classify(err))
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
			st.Status = StatusBaseline
			st.WeekKey = b.week.Key
			b.putState(repo, st)
			return repoOutcome{status: StatusBaseline}
		}
		st.LastTag = tags[0].Name
		for _, t := range tags {
			AddSeenTag(&st, t.Name)
			pool = append(pool, b.tagCandidate(ctx, repo, owner, name, t))
		}
	} else {
		st.Status = StatusBaseline
		st.WeekKey = b.week.Key
		b.putState(repo, st)
		return repoOutcome{status: StatusBaseline}
	}
	// The baseline itself writes no document: it records where "new" starts so
	// the first real week summarizes only what happened after subscribing.
	st.Status = StatusBaseline
	st.WeekKey = b.week.Key
	b.putState(repo, st)

	if b.opts.BackfillLatest <= 0 || len(pool) == 0 {
		return repoOutcome{status: StatusBaseline}
	}
	n := b.opts.BackfillLatest
	if n > len(pool) {
		n = len(pool)
	}
	group, summarized, degraded, truncated := b.summarizeRepo(ctx, repo, pool[:n])
	return repoOutcome{
		group:       group,
		status:      StatusOK,
		newReleases: n,
		summarized:  summarized,
		degraded:    degraded,
		truncated:   truncated,
	}
}

func (b *builder) fail(repo, class string) repoOutcome {
	status := StatusFailed
	if class == "ratelimited" {
		status = StatusRateLimited
	}
	// A failed check must not set WeekKey: the cursor did not move, so a retry
	// (explicit or the next scheduled run) has to be allowed to try again. The
	// status is still recorded so the panel can show 限流/失败 immediately.
	b.stamp(repo, status, false)
	return repoOutcome{status: status}
}

// stamp records the outcome of a check that advanced no cursor.
func (b *builder) stamp(repo, status string, done bool) {
	st, _ := b.getState(repo)
	st.Status = status
	now := time.Now().UTC()
	st.LastCheckedAt = &now
	if done {
		st.WeekKey = b.week.Key
	}
	b.putState(repo, st)
}

// getState is the lock-protected read that pairs with putState: workers run
// concurrently, and the cursor map is shared.
func (b *builder) getState(repo string) (RepoState, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state.Get(repo)
}

func (b *builder) putState(repo string, st RepoState) {
	if st.LastCheckedAt == nil {
		now := time.Now().UTC()
		st.LastCheckedAt = &now
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state.Set(repo, st)
}

// summarizeRepo makes exactly one model call for the whole week and renders the
// digest group from its single summary.
func (b *builder) summarizeRepo(ctx context.Context, repo string, cands []candidate) (*releasedigest.RepoReleases, int, int, int) {
	res := b.sum.SummarizeRepo(ctx, repo, b.week.Key, cands)
	items := make([]releasedigest.ReleaseItem, 0, len(cands))
	for _, c := range cands {
		item := releasedigest.ReleaseItem{
			ID:            c.rel.ID,
			Tag:           c.tagName(),
			Name:          c.rel.Name,
			Kind:          c.kind,
			PublishedAt:   c.rel.PublishedAt,
			URL:           c.rel.HTMLURL,
			Prerelease:    c.rel.Prerelease,
			Draft:         c.rel.Draft,
			SummarySource: res.Source,
		}
		if item.Name == "" {
			item.Name = item.Tag
		}
		if item.URL == "" {
			item.URL = repoURL(repo)
		}
		if c.rel.Body != "" {
			excerpt := truncateRunes(c.rel.Body, b.opts.MaxNotesChars)
			item.BodyExcerpt = &excerpt
		}
		items = append(items, item)
	}
	summary := res.SummaryZh
	start, end := b.week.Start, b.week.End
	group := &releasedigest.RepoReleases{
		FullName:    repo,
		URL:         repoURL(repo),
		LatestTag:   items[0].Tag,
		NewCount:    len(items),
		PeriodStart: &start,
		PeriodEnd:   &end,
		SummaryZh:   &summary,
		Releases:    items,
	}
	summarized, degraded, truncated := 0, 0, 0
	if res.Source == "model" {
		summarized = 1
	} else {
		degraded = 1
	}
	if res.Truncated {
		truncated = 1
	}
	return group, summarized, degraded, truncated
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
	b.mu.Lock()
	if b.commits == nil {
		b.commits = map[string]*fetch.Commit{}
	}
	cm, ok := b.commits[key]
	b.mu.Unlock()
	if ok {
		applyCommit(rel, sha, cm)
		return
	}
	fetched, err := b.fet.GetCommit(context.Background(), owner, name, sha)
	if err != nil {
		b.mu.Lock()
		b.commits[key] = nil
		b.mu.Unlock()
		return
	}
	b.mu.Lock()
	b.commits[key] = &fetched
	b.mu.Unlock()
	applyCommit(rel, sha, &fetched)
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

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
