package config

import (
	"errors"
	"testing"
)

func TestSummarizeDefaults(t *testing.T) {
	var c Config
	c.withDefaults()
	if c.Summarize.BatchSize != 10 {
		t.Fatalf("batchSize = %d, want 10", c.Summarize.BatchSize)
	}
	if c.Summarize.MaxRetries != 1 {
		t.Fatalf("maxRetries = %d, want 1", c.Summarize.MaxRetries)
	}
}

func TestSummarizeConfigOverrides(t *testing.T) {
	var c Config
	c.Summarize.BatchSize = 5
	c.Summarize.MaxRetries = 2
	c.withDefaults()
	if c.Summarize.BatchSize != 5 || c.Summarize.MaxRetries != 2 {
		t.Fatalf("override lost: %+v", c.Summarize)
	}
}

func TestSummarizeEnrichDefaults(t *testing.T) {
	var c Config
	c.withDefaults()
	s := c.Summarize
	if s.Enrich != "none" {
		t.Fatalf("enrich default = %q, want none", s.Enrich)
	}
	if s.ReadmeChars != 800 || s.EnrichCacheTTLHours != 168 || s.EnrichConcurrency != 4 || s.EnrichTimeoutSeconds != 5 || s.EnrichBudgetSeconds != 8 {
		t.Fatalf("unexpected enrich defaults: %+v", s)
	}
}

func TestSummarizeEnrichReadmeBudget(t *testing.T) {
	var c Config
	c.Summarize.Enrich = "readme"
	c.withDefaults()
	if c.Summarize.EnrichBudgetSeconds != 12 {
		t.Fatalf("readme budget = %d, want 12", c.Summarize.EnrichBudgetSeconds)
	}
}

func TestValidateRejectsBadEnrich(t *testing.T) {
	c := &Config{
		Model:    ModelConfig{Provider: "deepseek", Name: "m", BaseURL: "https://x.example/v1"},
		Repo:     RepoConfig{Path: "/tmp/repo"},
		Trending: TrendingConfig{Languages: []string{"all"}, Since: "daily"},
		Output:   OutputConfig{Dir: "digests"},
	}
	c.Summarize.Enrich = "bogus"
	if err := c.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for enrich=bogus, got %v", err)
	}
}

func TestValidateAcceptsEnrichModes(t *testing.T) {
	for _, mode := range []string{"none", "repo", "readme"} {
		c := &Config{
			Model:    ModelConfig{Provider: "deepseek", Name: "m", BaseURL: "https://x.example/v1"},
			Repo:     RepoConfig{Path: "/tmp/repo"},
			Trending: TrendingConfig{Languages: []string{"all"}, Since: "daily"},
			Output:   OutputConfig{Dir: "digests"},
		}
		c.Summarize.Enrich = mode
		if err := c.Validate(); err != nil {
			t.Fatalf("enrich=%q should validate: %v", mode, err)
		}
	}
}
