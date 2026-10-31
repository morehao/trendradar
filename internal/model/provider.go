// Package model abstracts the chat model used for summarization. It exposes a
// small Provider interface so the rest of the program can run offline against a
// Fake provider while production uses golib/gllm.
package model

import "context"

// Usage is the token cost of one or more completions. Known is false when the
// provider did not report usage (e.g. the offline Fake provider or a degraded
// fallback that made no real request), so callers can tell "0 tokens" apart from
// "unknown tokens".
type Usage struct {
	PromptTokens     int  `json:"promptTokens"`
	CompletionTokens int  `json:"completionTokens"`
	TotalTokens      int  `json:"totalTokens"`
	CachedTokens     int  `json:"cachedTokens,omitempty"`
	Known            bool `json:"known"`
}

// Result is one completion.
type Result struct {
	Text     string
	Degraded bool  // provider signalled it fell back (e.g. missing API key)
	Usage    Usage // token usage reported by the provider (Known=false if absent)
}

// Provider performs chat completions.
type Provider interface {
	Complete(ctx context.Context, system, user string) (Result, error)
	Name() string
}
