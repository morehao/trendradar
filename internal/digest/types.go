// Package digest holds the domain types shared by fetch, parse and render. It is
// a thin layer over the shared contract package: the wire types ARE the domain
// types (single source of truth), plus the internal-only ParseReport and a few
// pointer helpers for optional fields.
package digest

import contract "github.com/morehao/trendradar/contract"

// Source records where a snapshot came from (string alias of the contract enum).
type Source = string

const (
	SourceTrendingHTML Source = "trending-html"
	SourceSearchAPI    Source = "search-api"
)

// Aliases to the contract wire types.
type (
	RepoSnapshot = contract.RepoSnapshot
	RepoDigest   = contract.RepoDigest
	Digest       = contract.Digest
)

// ParseReport summarizes parsing quality (internal diagnostic, not on the wire).
type ParseReport struct {
	Total           int     `json:"total"`
	MissingFullName int     `json:"missingFullName"`
	MissingURL      int     `json:"missingUrl"`
	MissingRate     float64 `json:"missingRate"`
	Degraded        bool    `json:"degraded"`
}

// StrPtr returns a pointer to s, or nil when s is empty (optional field omitted).
func StrPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// IntPtr returns a pointer to i, or nil when i is zero (optional field omitted).
func IntPtr(i int) *int {
	if i == 0 {
		return nil
	}
	return &i
}

// Str dereferences an optional string (nil -> "").
func Str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Int dereferences an optional int (nil -> 0).
func Int(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
