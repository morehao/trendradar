// Package summarize turns repo snapshots into summaries, batching requests and
// degrading per batch (never failing the whole run because one batch failed).
package summarize

import (
	"context"
	"sync"

	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/model"
)

// Stats reports token consumption and call counts for a Summarize call. The
// counts are per-run diagnostics: Calls includes every real provider request
// (including retries) so token spend is not undercounted.
type Stats struct {
	Usage     model.Usage `json:"usage"`
	Calls     int         `json:"calls"`     // real provider calls, retries included
	Retries   int         `json:"retries"`   // retry attempts after the first per batch
	Batches   int         `json:"batches"`   // batches processed
	CacheHits int         `json:"cacheHits"` // repos served from cache (0 tokens)
}

// Summarizer batches summarization calls.
type Summarizer struct {
	chat       model.Provider
	batchSize  int
	maxRetries int
	cache      *Cache

	mu    sync.Mutex
	stats Stats
}

// Option customizes a Summarizer.
type Option func(*Summarizer)

// WithBatchSize sets the number of repos per model call (default 10).
func WithBatchSize(n int) Option {
	return func(s *Summarizer) {
		if n > 0 {
			s.batchSize = n
		}
	}
}

// WithMaxRetries sets extra attempts after the first failure (default 1).
func WithMaxRetries(n int) Option {
	return func(s *Summarizer) {
		if n >= 0 {
			s.maxRetries = n
		}
	}
}

// WithCache attaches a cache.
func WithCache(c *Cache) Option { return func(s *Summarizer) { s.cache = c } }

// New builds a Summarizer.
func New(chat model.Provider, opts ...Option) *Summarizer {
	s := &Summarizer{chat: chat, batchSize: 10, maxRetries: 1}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Summarize produces a RepoDigest per input snapshot. Failed batches degrade to
// the original description (source "original").
func (s *Summarizer) Summarize(ctx context.Context, repos []digest.RepoSnapshot) []digest.RepoDigest {
	s.mu.Lock()
	s.stats = Stats{}
	s.mu.Unlock()

	out := make([]digest.RepoDigest, 0, len(repos))
	for start := 0; start < len(repos); start += s.batchSize {
		end := start + s.batchSize
		if end > len(repos) {
			end = len(repos)
		}
		batch := repos[start:end]
		s.mu.Lock()
		s.stats.Batches++
		s.mu.Unlock()
		sums := s.summarizeBatch(ctx, batch)
		for _, r := range batch {
			if sum, ok := sums[r.FullName]; ok && sum != "" {
				out = append(out, digest.RepoDigest{RepoSnapshot: r, SummaryZh: sum, SummarySource: "model"})
			} else {
				out = append(out, original(r))
			}
		}
	}
	return out
}

func (s *Summarizer) summarizeBatch(ctx context.Context, batch []digest.RepoSnapshot) map[string]string {
	out := make(map[string]string, len(batch))
	var pending []digest.RepoSnapshot
	for _, r := range batch {
		if sum, ok := s.cache.Get(r.FullName, digest.Str(r.Description)); ok {
			out[r.FullName] = sum
			s.addCacheHit()
			continue
		}
		pending = append(pending, r)
	}
	if len(pending) == 0 {
		return out
	}

	system, user := SystemPrompt, buildUserPrompt(pending)
	for attempt := 0; attempt <= s.maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			break
		}
		res, err := s.chat.Complete(ctx, system, user)
		// Count the attempt even when it errors or later fails to parse: a real
		// request was made and billed.
		s.addCall(res.Usage, attempt > 0)
		if err != nil {
			continue
		}
		sums, perr := parseResponse(res.Text, pending)
		if perr != nil {
			continue
		}
		for _, r := range pending {
			if v, ok := sums[r.FullName]; ok {
				out[r.FullName] = v
				s.cache.Put(r.FullName, digest.Str(r.Description), v)
			}
		}
		return out
	}
	return out
}

// Stats returns a snapshot of the counters accumulated by the last Summarize
// call (reset at its start). Safe to call from another goroutine.
func (s *Summarizer) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

func (s *Summarizer) addCacheHit() {
	s.mu.Lock()
	s.stats.CacheHits++
	s.mu.Unlock()
}

func (s *Summarizer) addCall(u model.Usage, retry bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.Calls++
	if retry {
		s.stats.Retries++
	}
	if !u.Known {
		return
	}
	total := u.TotalTokens
	if total == 0 {
		total = u.PromptTokens + u.CompletionTokens
	}
	s.stats.Usage.Known = true
	s.stats.Usage.PromptTokens += u.PromptTokens
	s.stats.Usage.CompletionTokens += u.CompletionTokens
	s.stats.Usage.TotalTokens += total
	s.stats.Usage.CachedTokens += u.CachedTokens
}

func original(r digest.RepoSnapshot) digest.RepoDigest {
	desc := digest.Str(r.Description)
	if desc == "" {
		desc = "(无描述)"
	}
	return digest.RepoDigest{RepoSnapshot: r, SummaryZh: "[原文] " + desc, SummarySource: "original"}
}
