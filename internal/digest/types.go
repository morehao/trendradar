// Package digest holds the domain types shared by fetch, parse and render.
package digest

// Source records where a snapshot came from.
type Source string

const (
	SourceTrendingHTML Source = "trending-html"
	SourceSearchAPI    Source = "search-api"
)

// RepoSnapshot is one repository as scraped from a trending page (or the search
// API fallback). It is the input to summarization and rendering.
type RepoSnapshot struct {
	FullName      string `json:"fullName"`
	URL           string `json:"url"`
	Description   string `json:"description,omitempty"`
	Language      string `json:"language"`
	Stars         int    `json:"stars"`
	StarsInPeriod int    `json:"starsInPeriod"`
	Forks         int    `json:"forks"`
	Rank          int    `json:"rank"`
	Source        Source `json:"source"`
}

// ParseReport summarizes parsing quality. Degraded is set when required fields
// are missing above the configured rate.
type ParseReport struct {
	Total           int     `json:"total"`
	MissingFullName int     `json:"missingFullName"`
	MissingURL      int     `json:"missingUrl"`
	MissingRate     float64 `json:"missingRate"`
	Degraded        bool    `json:"degraded"`
}
