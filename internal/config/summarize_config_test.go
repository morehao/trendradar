package config

import "testing"

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
