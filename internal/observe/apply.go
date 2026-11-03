package observe

import (
	"context"
	"fmt"
	"strings"

	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/fetch"
)

// Fetcher supplies authoritative current star counts (and optional history) from
// GitHub. It is satisfied by *fetch.Client; defined as an interface so the
// observation pipeline can be unit-tested without network access, and so ingest
// stays decoupled from the GraphQL client implementation.
type Fetcher interface {
	// FetchStars returns current stargazers/forks keyed by "owner/name". A repo
	// absent from the map (deleted/renamed) is skipped by the caller.
	FetchStars(ctx context.Context, repos []string) (map[string]fetch.StarCount, error)
	// BackfillHistory returns weekly star buckets for one repo, or an error. It is
	// best-effort: a failure only means we start accumulating from today.
	BackfillHistory(ctx context.Context, fullName string) ([]fetch.StarHistoryPoint, error)
}

// ApplyOptions configures one observation pass.
type ApplyOptions struct {
	Date          string // UTC "YYYY-MM-DD"
	Source        string // "trending-html" | "search-api" ...
	Period        string // "daily"|"weekly"|"monthly" -> which delta feeds StarsInPeriod
	MaxCandidates int    // cap how many repos are recorded (plan D2); <=0 means no cap
	BackfillDays  int    // >0 enables first-seen backfill via Fetcher; 0 disables
}

// pickDelta returns the delta that matches the digest period. starsInPeriod means
// "stars over the digest period", so a daily digest uses d1, weekly d7, monthly
// d30. Unknown/empty defaults to d1.
func (o ApplyOptions) pickDelta(d Deltas) Delta {
	switch o.Period {
	case "weekly":
		return d.D7
	case "monthly":
		return d.D30
	default:
		return d.D1
	}
}

// ApplyResult reports what the observation pass did, for the run report/warnings.
type ApplyResult struct {
	Recorded      int // snapshots written for this run
	WithDelta     int // repos that got a real d1 written into StarsInPeriod
	NoBaseline    int // repos with no d1 baseline (StarsInPeriod left unchanged)
	Backfilled    int // first-seen repos seeded from history
	BackfillError int // first-seen repos whose backfill failed (non-fatal)
	Skipped       int // corrupt store lines skipped during load
	Warnings      []string
}

// Apply records this run's star snapshots and fills StarsInPeriod with a real
// observed delta where a baseline exists. It is the single integration point
// between ingest and the observation store.
//
// The returned RepoSnapshots are the same slice, mutated in place for
// StarsInPeriod. When date is empty, a store error occurs, or a repo has no
// recordable data, the corresponding snaps are left exactly as ingested — so a
// caller that disables the store, or whose store is unavailable, gets byte-for-
// byte v0.8.7 behaviour.
//
// fetcher may be nil: then the page-supplied Stars value is used as the current
// count and no backfill is attempted. This keeps the pass working offline.
func Apply(ctx context.Context, s *Store, repos []digest.RepoSnapshot, opts ApplyOptions, fetcher Fetcher) ([]digest.RepoSnapshot, ApplyResult, error) {
	var res ApplyResult
	if s == nil || strings.TrimSpace(opts.Date) == "" {
		return repos, res, nil
	}

	candidates := repos
	if opts.MaxCandidates > 0 && len(candidates) > opts.MaxCandidates {
		candidates = candidates[:opts.MaxCandidates]
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"observe: capped at %d/%d candidates (observe.maxCandidates)", opts.MaxCandidates, len(repos)))
	}

	// Resolve current counts. Prefer the fetcher's authoritative counts; fall back
	// to the page value for repos the fetcher did not return.
	current := make(map[string]fetch.StarCount, len(candidates))
	var stars map[string]fetch.StarCount
	if fetcher != nil {
		names := make([]string, 0, len(candidates))
		for _, r := range candidates {
			names = append(names, r.FullName)
		}
		got, err := fetcher.FetchStars(ctx, names)
		if err != nil {
			// Non-fatal: fall back to page values for the whole batch.
			res.Warnings = append(res.Warnings, "observe: FetchStars failed, using page star counts: "+err.Error())
		}
		stars = got
	}
	for _, r := range candidates {
		if sc, ok := stars[r.FullName]; ok {
			current[r.FullName] = sc
		} else {
			current[r.FullName] = fetch.StarCount{FullName: r.FullName, Stars: r.Stars, Forks: digest.Int(r.Forks)}
		}
	}

	for _, r := range candidates {
		sc := current[r.FullName]
		repoID := r.FullName // stable key: full name (rename handled as new repo)
		firstSeen := false
		if _, existed, err := s.Put(Snapshot{
			RepoID: repoID, Date: opts.Date, Stars: sc.Stars, Forks: sc.Forks, Source: opts.Source,
		}); err != nil {
			res.Warnings = append(res.Warnings, "observe: write snapshot for "+repoID+": "+err.Error())
			continue
		} else if !existed {
			firstSeen = true
		}
		res.Recorded++

		// First-seen backfill (best-effort, before computing deltas so d7/d30 can
		// use seeded history).
		if firstSeen && fetcher != nil && opts.BackfillDays > 0 {
			if err := backfill(ctx, s, fetcher, repoID, opts.BackfillDays); err != nil {
				res.BackfillError++
				res.Warnings = append(res.Warnings, "observe: backfill "+repoID+": "+err.Error())
			} else {
				res.Backfilled++
			}
		}
	}

	// Second pass: compute deltas and write StarsInPeriod back in place.
	byName := make(map[string]int, len(candidates))
	for i, r := range candidates {
		byName[r.FullName] = i
	}
	for i := range repos {
		if _, ok := byName[repos[i].FullName]; !ok {
			continue // was capped out
		}
		deltas, err := s.Compute(repos[i].FullName, opts.Date)
		if err != nil {
			res.Warnings = append(res.Warnings, "observe: compute delta for "+repos[i].FullName+": "+err.Error())
			continue
		}
		d := opts.pickDelta(deltas)
		if d.Value != nil {
			// StarsInPeriod is an int on the wire (contract v2), so a real observed
			// delta — including a negative one — replaces the page number. When no
			// baseline exists we leave the ingested value untouched (never write a
			// fabricated 0); plan invariant 2 / AC-2.
			repos[i].StarsInPeriod = *d.Value
			res.WithDelta++
		} else {
			res.NoBaseline++
		}
	}
	res.Skipped = s.Skipped()
	if res.Skipped > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("observe: skipped %d corrupt store line(s)", res.Skipped))
	}
	return repos, res, nil
}

// backfill seeds a repo's history from weekly buckets, never overwriting a real
// (non-backfill) snapshot for the same date. The history endpoint already scopes
// its window, so we store every bucket it returns, marked source="backfill" so a
// later live observation on the same date wins (dedupe keeps the newest write).
func backfill(ctx context.Context, s *Store, fetcher Fetcher, repoID string, backfillDays int) error {
	pts, err := fetcher.BackfillHistory(ctx, repoID)
	if err != nil {
		return err
	}
	if len(pts) == 0 {
		return nil
	}
	for _, p := range pts {
		if strings.TrimSpace(p.WeekStart) == "" {
			continue
		}
		if _, existed, err := s.Lookup(repoID, p.WeekStart); err != nil {
			return err
		} else if existed {
			continue // never overwrite an existing (real) observation
		}
		if _, _, err := s.Put(Snapshot{
			RepoID: repoID, Date: p.WeekStart, Stars: p.Stars, Source: "backfill",
		}); err != nil {
			return err
		}
	}
	return nil
}
