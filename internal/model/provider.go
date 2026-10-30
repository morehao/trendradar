// Package model abstracts the chat model used for summarization. It exposes a
// small Provider interface so the rest of the program can run offline against a
// Fake provider while production uses golib/gllm.
package model

import "context"

// Result is one completion.
type Result struct {
	Text     string
	Degraded bool // provider signalled it fell back (e.g. missing API key)
}

// Provider performs chat completions.
type Provider interface {
	Complete(ctx context.Context, system, user string) (Result, error)
	Name() string
}
