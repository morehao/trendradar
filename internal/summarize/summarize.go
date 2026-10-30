// Package summarize turns repo snapshots into summaries, batching requests and
// degrading per batch (never failing the whole run because one batch failed).
package summarize

import (
	"context"

	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/model"
)

// Summarizer batches summarization calls.
type Summarizer struct {
	chat       model.Provider
	batchSize  int
	maxRetries int
	cache      *Cache
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
	out := make([]digest.RepoDigest, 0, len(repos))
	for start := 0; start < len(repos); start += s.batchSize {
		end := start + s.batchSize
		if end > len(repos) {
			end = len(repos)
		}
		batch := repos[start:end]
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

func original(r digest.RepoSnapshot) digest.RepoDigest {
	desc := digest.Str(r.Description)
	if desc == "" {
		desc = "(无描述)"
	}
	return digest.RepoDigest{RepoSnapshot: r, SummaryZh: "[原文] " + desc, SummarySource: "original"}
}
