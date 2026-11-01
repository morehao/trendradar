package app

import (
	"context"
	"sync"
	"testing"

	"github.com/morehao/trendradar/internal/model"
)

// forceCountingProvider counts how many Complete calls reach the model, so we
// can assert whether the persisted summary cache was consulted.
type forceCountingProvider struct {
	inner model.Provider
	mu    sync.Mutex
	calls int
}

func (p *forceCountingProvider) Name() string { return "force-counting" }

func (p *forceCountingProvider) Complete(ctx context.Context, system, user string) (model.Result, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return p.inner.Complete(ctx, system, user)
}

func (p *forceCountingProvider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// A non-forced second run must hit the persisted cache: the model is asked only
// once across two runs with the same input.
func TestForceOffReusesCache(t *testing.T) {
	_, _, cfg := setupRepo(t)
	in := fixtureInputs()

	first := &forceCountingProvider{inner: model.NewFake()}
	if _, err := Run(context.Background(), cfg, in, Options{Provider: first}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if first.Calls() == 0 {
		t.Fatal("first run should call the model at least once")
	}

	second := &forceCountingProvider{inner: model.NewFake()}
	if _, err := Run(context.Background(), cfg, in, Options{Provider: second}); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.Calls() != 0 {
		t.Fatalf("forced=false second run called the model %d times, want 0 (cache hit)", second.Calls())
	}
}

// A forced run must ignore the persisted cache and re-ask the model, even when
// the first run warmed it.
func TestForceBypassesCache(t *testing.T) {
	_, _, cfg := setupRepo(t)
	in := fixtureInputs()

	warm := &forceCountingProvider{inner: model.NewFake()}
	if _, err := Run(context.Background(), cfg, in, Options{Provider: warm}); err != nil {
		t.Fatalf("warm run: %v", err)
	}
	if warm.Calls() == 0 {
		t.Fatal("warm run should call the model")
	}

	forced := &forceCountingProvider{inner: model.NewFake()}
	if _, err := Run(context.Background(), cfg, in, Options{Provider: forced, Force: true}); err != nil {
		t.Fatalf("forced run: %v", err)
	}
	if forced.Calls() == 0 {
		t.Fatal("forced run must re-ask the model, but it hit the cache")
	}
}

// Even with Force, identical rendered artifacts must NOT create a second
// commit: force changes cache behaviour, not the append-only skip decision.
func TestForceStillSkipsWhenContentIdentical(t *testing.T) {
	remote, _, cfg := setupRepo(t)
	in := fixtureInputs()

	if _, err := Run(context.Background(), cfg, in, Options{Provider: model.NewFake()}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	base := countRef(t, remote, "trending/daily/2026-10-30")

	r2, err := Run(context.Background(), cfg, in, Options{Provider: model.NewFake(), Force: true})
	if err != nil {
		t.Fatalf("forced run: %v", err)
	}
	if !r2.Skipped || r2.SkipReason != "unchanged" {
		t.Fatalf("forced identical run should skip unchanged, got skipped=%v reason=%q", r2.Skipped, r2.SkipReason)
	}
	if got := countRef(t, remote, "trending/daily/2026-10-30"); got != base {
		t.Fatalf("forced identical run added commits: got %d, want %d", got, base)
	}
}
