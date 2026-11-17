package releasewatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	contract "github.com/morehao/trendradar/contract"
	"github.com/morehao/trendradar/internal/releasedoc"
)

// validStatus guards the local cursor's status against the contract enum: an
// unknown value (hand-edited state file, future writer) degrades to baseline
// instead of emitting a row the schema would reject.
func validStatus(s string) bool {
	switch s {
	case StatusBaseline, StatusOK, StatusNoChange, StatusRateLimited, StatusFailed:
		return true
	}
	return false
}

// Items builds the per-repo rows of the subscriptions table: the last tag, the
// last summary date, when the repo was last checked and how that check went.
//
// Everything comes from files the core already owns — the machine-local cursor
// and the release document index — so building the table costs no network and no
// document read. A repo with no cursor yet reports "baseline", which is exactly
// "subscribed, not checked yet".
func Items(statePath, repoRoot, docRoot string, repos []string) []contract.SubscriptionItem {
	items := make([]contract.SubscriptionItem, 0, len(repos))
	// A corrupt or unreadable cursor must not break `subscribe list`: the repos
	// list is the contract's only required field, so degrade to baseline rows.
	state, err := LoadState(statePath)
	if err != nil {
		state = NewState()
	}
	for _, repo := range repos {
		url := repoURL(repo)
		item := contract.SubscriptionItem{
			FullName: repo,
			URL:      &url,
			Status:   contract.SubscriptionStatus(StatusBaseline),
		}
		st, ok := state.Get(repo)
		if ok {
			if st.LastTag != "" {
				tag := st.LastTag
				item.LatestTag = &tag
			}
			if st.LastCheckedAt != nil {
				item.LastCheckedAt = st.LastCheckedAt
			}
			if validStatus(st.Status) {
				item.Status = contract.SubscriptionStatus(st.Status)
			}
			if st.LatestSummaryAt != "" {
				at := st.LatestSummaryAt
				item.LatestSummaryAt = &at
			}
		}
		// The document is the durable record: a fresh machine has no cursor but
		// still has the committed markdown, so fall back to its newest week.
		if item.LatestSummaryAt == nil {
			if at, ok := releasedoc.Latest(repoRoot, docRoot, repo); ok {
				item.LatestSummaryAt = &at
			}
		}
		// Reconcile LatestTag against the durable, dated record. A cursor written
		// by an older core can pin a rolling prerelease (ragflow's "nightly":
		// fresh created_at, year-old publishedAt) as latest even after a newer
		// production release was summarized, and that cursor never heals itself
		// while no new version arrives. The newest weekly digest carries each
		// version's real publish date, so the true newest is recoverable offline.
		// Only override when the digest actually has a dated, newer version.
		if tag, ok := newestDigestTag(repoRoot, docRoot, repo); ok && tag != "" && tag != derefOr(item.LatestTag, "") {
			latest := tag
			item.LatestTag = &latest
		}
		items = append(items, item)
	}
	return items
}

func derefOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}

// weeklyDigestFile mirrors the subset of the release digest JSON Items needs.
type weeklyDigestFile struct {
	Repos []struct {
		FullName  string `json:"fullName"`
		LatestTag string `json:"latestTag"`
		Releases  []struct {
			Tag         string     `json:"tag"`
			Name        string     `json:"name"`
			PublishedAt *time.Time `json:"publishedAt"`
		} `json:"releases"`
	} `json:"repos"`
}

// newestDigestTag returns, for one repo, the tag of the version with the
// greatest publish date across the weekly digest JSONs under docRoot (newest
// file first, stopping at the first file that lists the repo and has a dated
// version). It is a best-effort offline reconciliation: a missing or malformed
// digest simply yields ok=false and the caller keeps the cursor value.
//
// docRoot is repository-relative in the CLI path and absolute under serve, the
// same convention releasedoc uses: join it under repoRoot only when relative.
func newestDigestTag(repoRoot, docRoot, repo string) (string, bool) {
	if docRoot == "" {
		return "", false
	}
	base := docRoot
	if !filepath.IsAbs(base) {
		base = filepath.Join(repoRoot, filepath.FromSlash(base))
	}
	dirs, err := filepath.Glob(filepath.Join(base, "_weekly", "*", "*.json"))
	if err != nil || len(dirs) == 0 {
		return "", false
	}
	// Newest week first: the filename is <YYYY-Wnn>.json, so lexical sort of the
	// basename within an ISO-year directory matches chronological order.
	sort.SliceStable(dirs, func(i, j int) bool { return dirs[i] > dirs[j] })
	for _, file := range dirs {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var d weeklyDigestFile
		if json.Unmarshal(data, &d) != nil {
			continue
		}
		for _, r := range d.Repos {
			if r.FullName != repo {
				continue
			}
			bestTag := ""
			var bestAt time.Time
			for _, rel := range r.Releases {
				if rel.PublishedAt == nil {
					continue
				}
				tag := rel.Tag
				if tag == "" {
					tag = rel.Name
				}
				if tag == "" {
					continue
				}
				if bestTag == "" || rel.PublishedAt.After(bestAt) {
					bestTag = tag
					bestAt = *rel.PublishedAt
				}
			}
			if bestTag != "" {
				return bestTag, true
			}
			// Repo present but no dated versions in this file: fall through to
			// older files (an older week may still carry the only dated version).
		}
	}
	return "", false
}
