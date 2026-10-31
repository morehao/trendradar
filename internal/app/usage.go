package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// UsageRecord is one line in the usage log: the token counts and call counters
// of a single run. It is intentionally flat so the file can be tailed/parsed by
// simple tools and later aggregated.
type UsageRecord struct {
	Time             time.Time `json:"time"`
	Provider         string    `json:"provider,omitempty"`
	Model            string    `json:"model,omitempty"`
	ContentHash      string    `json:"contentHash,omitempty"`
	PromptTokens     int       `json:"promptTokens"`
	CompletionTokens int       `json:"completionTokens"`
	TotalTokens      int       `json:"totalTokens"`
	CachedTokens     int       `json:"cachedTokens,omitempty"`
	UsageKnown       bool      `json:"usageKnown"`
	Calls            int       `json:"calls"`
	Retries          int       `json:"retries"`
	Batches          int       `json:"batches"`
	CacheHits        int       `json:"cacheHits"`
}

// UsageLogPath returns <repo>/.git/trendradar-usage.jsonl. Keeping it under
// .git means it is never committed and is scoped to this clone.
func UsageLogPath(repoPath string) string {
	return filepath.Join(repoPath, ".git", "trendradar-usage.jsonl")
}

// AppendUsageLog appends one JSON line to the run's usage log. A missing .git
// directory (not a git work tree) is a no-op: usage logging is an optimization
// and must never fail a run.
func AppendUsageLog(repoPath string, rec UsageRecord) error {
	if repoPath == "" {
		return nil
	}
	gitDir := filepath.Join(repoPath, ".git")
	if fi, err := os.Stat(gitDir); err != nil || !fi.IsDir() {
		return nil
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(UsageLogPath(repoPath), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}
