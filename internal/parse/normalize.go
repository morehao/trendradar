package parse

import (
	"sort"
	"strings"

	"github.com/morehao/trendradar/internal/digest"
)

// normalizeLanguage lowercases a language name; empty becomes "unknown".
func normalizeLanguage(lang string) string {
	l := strings.ToLower(strings.TrimSpace(lang))
	if l == "" {
		return "unknown"
	}
	return l
}

// Merge de-duplicates snapshots by fullName across pages (keeping the highest
// rank, i.e. the smallest rank number) and reassigns contiguous ranks.
func Merge(pages [][]digest.RepoSnapshot) []digest.RepoSnapshot {
	best := make(map[string]digest.RepoSnapshot)
	for _, page := range pages {
		for _, r := range page {
			cur, exists := best[r.FullName]
			if !exists || r.Rank < cur.Rank {
				best[r.FullName] = r
			}
		}
	}
	out := make([]digest.RepoSnapshot, 0, len(best))
	for _, r := range best {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rank != out[j].Rank {
			return out[i].Rank < out[j].Rank
		}
		return out[i].FullName < out[j].FullName
	})
	for i := range out {
		out[i].Rank = i + 1
	}
	return out
}

func sortStrings(s []string) { sort.Strings(s) }
