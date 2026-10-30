package digest

import "time"

// RepoDigest is a snapshot enriched with a Chinese summary.
type RepoDigest struct {
	RepoSnapshot
	SummaryZh     string `json:"summaryZh"`
	SummarySource string `json:"summarySource"` // "model" | "original"
}

// Digest is one day's rendered document (matches contract/schema/digest.v1.json).
type Digest struct {
	Schema      int          `json:"schema"`
	Date        string       `json:"date"`
	Period      string       `json:"period"`
	Languages   []string     `json:"languages"`
	Count       int          `json:"count"`
	Degraded    bool         `json:"degraded"`
	ContentHash string       `json:"contentHash"`
	CoreVersion string       `json:"coreVersion"`
	GeneratedAt time.Time    `json:"generatedAt"`
	Repos       []RepoDigest `json:"repos"`
}
