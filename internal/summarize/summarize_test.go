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
		FullName: name, URL: "https://github.com/" + name, Description: "desc " + name,
		Language: "go", Stars: 100, StarsInPeriod: 10, Forks: 5, Rank: i + 1, Source: digest.SourceTrendingHTML,
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
