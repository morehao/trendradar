package releasewatch

import (
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
		items = append(items, item)
	}
	return items
}
