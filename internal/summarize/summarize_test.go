package summarize

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/model"
)

func snap(i int) digest.RepoSnapshot {
	name := fmt.Sprintf("acme/repo%d", i)
	return digest.RepoSnapshot{
		FullName: name, URL: "https://github.com/" + name, Description: digest.StrPtr("desc " + name),
		Language: "go", Stars: 100, StarsInPeriod: 10, Forks: digest.IntPtr(5), Rank: i + 1, Source: digest.SourceTrendingHTML,
	}
}

func snaps(n int) []digest.RepoSnapshot {
	out := make([]digest.RepoSnapshot, n)
	for i := range out {
		out[i] = snap(i)
	}
	return out
}

type countingProvider struct {
	inner model.Provider
	mu    sync.Mutex
	calls int
}

func (c *countingProvider) Name() string { return "counting" }
func (c *countingProvider) Complete(ctx context.Context, s, u string) (model.Result, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.inner.Complete(ctx, s, u)
}

type badProvider struct{}

func (badProvider) Name() string { return "bad" }
func (badProvider) Complete(context.Context, string, string) (model.Result, error) {
	return model.Result{Text: "this is not json"}, nil
}

func TestSummarizeUsesModel(t *testing.T) {
	s := New(model.NewFake())
	out := s.Summarize(context.Background(), snaps(12))
	if len(out) != 12 {
		t.Fatalf("len = %d, want 12", len(out))
	}
	for _, d := range out {
		if d.SummarySource != "model" {
			t.Fatalf("%s source = %s, want model", d.FullName, d.SummarySource)
		}
		if !strings.HasPrefix(d.SummaryZh, "[fake]") {
			t.Fatalf("%s summary = %q", d.FullName, d.SummaryZh)
		}
	}
}

func TestSummarizeBatchCount(t *testing.T) {
	cp := &countingProvider{inner: model.NewFake()}
	s := New(cp, WithBatchSize(10))
	s.Summarize(context.Background(), snaps(12))
	if cp.calls != 2 {
		t.Fatalf("calls = %d, want 2 (10+2)", cp.calls)
	}
}

func TestSummarizeDegradesOnBadOutput(t *testing.T) {
	s := New(badProvider{}, WithMaxRetries(1))
	out := s.Summarize(context.Background(), snaps(3))
	for _, d := range out {
		if d.SummarySource != "original" {
			t.Fatalf("%s source = %s, want original", d.FullName, d.SummarySource)
		}
		if !strings.HasPrefix(d.SummaryZh, "[原文]") {
			t.Fatalf("%s summary = %q", d.FullName, d.SummaryZh)
		}
	}
}

func TestCacheAvoidsSecondCall(t *testing.T) {
	cp := &countingProvider{inner: model.NewFake()}
	s := New(cp, WithCache(NewCache()))
	repos := snaps(5)
	s.Summarize(context.Background(), repos)
	if cp.calls != 1 {
		t.Fatalf("first run calls = %d, want 1", cp.calls)
	}
	out := s.Summarize(context.Background(), repos)
	if cp.calls != 1 {
		t.Fatalf("second run calls = %d, want still 1 (cache hit)", cp.calls)
	}
	for _, d := range out {
		if d.SummarySource != "model" {
			t.Fatalf("cached %s source = %s", d.FullName, d.SummarySource)
		}
	}
}

// usageProvider delegates to another provider but reports a fixed usage, so
// token aggregation can be asserted deterministically.
type usageProvider struct {
	inner model.Provider
	usage model.Usage
}

func (p *usageProvider) Name() string { return "usage" }
func (p *usageProvider) Complete(ctx context.Context, s, u string) (model.Result, error) {
	res, err := p.inner.Complete(ctx, s, u)
	res.Usage = p.usage
	return res, err
}

type usageBadProvider struct{ usage model.Usage }

func (p *usageBadProvider) Name() string { return "usage-bad" }
func (p *usageBadProvider) Complete(context.Context, string, string) (model.Result, error) {
	return model.Result{Text: "not json", Usage: p.usage}, nil
}

func TestStatsAggregatesUsage(t *testing.T) {
	p := &usageProvider{
		inner: model.NewFake(),
		usage: model.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, Known: true},
	}
	s := New(p, WithBatchSize(10))
	if out := s.Summarize(context.Background(), snaps(12)); len(out) != 12 {
		t.Fatalf("len = %d, want 12", len(out))
	}
	st := s.Stats()
	if st.Calls != 2 || st.Batches != 2 || st.Retries != 0 {
		t.Fatalf("calls/batches/retries = %d/%d/%d, want 2/2/0", st.Calls, st.Batches, st.Retries)
	}
	if !st.Usage.Known {
		t.Fatal("usage should be known")
	}
	if st.Usage.PromptTokens != 20 || st.Usage.CompletionTokens != 10 || st.Usage.TotalTokens != 30 {
		t.Fatalf("usage = %+v, want prompt=20 completion=10 total=30", st.Usage)
	}
}

func TestStatsCountsRetriesAndBilledAttempts(t *testing.T) {
	p := &usageBadProvider{usage: model.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10, Known: true}}
	s := New(p, WithMaxRetries(1))
	out := s.Summarize(context.Background(), snaps(3))
	for _, d := range out {
		if d.SummarySource != "original" {
			t.Fatalf("%s source = %s, want original", d.FullName, d.SummarySource)
		}
	}
	st := s.Stats()
	if st.Calls != 2 {
		t.Fatalf("calls = %d, want 2 (first + 1 retry)", st.Calls)
	}
	if st.Retries != 1 {
		t.Fatalf("retries = %d, want 1", st.Retries)
	}
	if st.Usage.TotalTokens != 20 {
		t.Fatalf("total tokens = %d, want 20 (both attempts billed)", st.Usage.TotalTokens)
	}
}

func TestStatsCountsCacheHits(t *testing.T) {
	cp := &countingProvider{inner: model.NewFake()}
	s := New(cp, WithCache(NewCache()))
	repos := snaps(5)
	s.Summarize(context.Background(), repos)
	st1 := s.Stats()
	if st1.Calls != 1 || st1.CacheHits != 0 {
		t.Fatalf("first run calls/cacheHits = %d/%d, want 1/0", st1.Calls, st1.CacheHits)
	}
	s.Summarize(context.Background(), repos)
	st2 := s.Stats()
	if st2.Calls != 0 || st2.CacheHits != 5 {
		t.Fatalf("second run calls/cacheHits = %d/%d, want 0/5", st2.Calls, st2.CacheHits)
	}
	if st2.Batches != 1 {
		t.Fatalf("second run batches = %d, want 1", st2.Batches)
	}
}
