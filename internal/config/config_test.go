package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

const validYAML = `
model:
  provider: deepseek
  name: deepseek-chat
  baseURL: https://api.deepseek.com/v1
  apiKey: test-key
repo:
  path: /tmp/repo
pr:
  enabled: true
trending:
  languages: [all, go]
  since: daily
output:
  dir: digests
`

func TestLoadValid(t *testing.T) {
	c, err := Load(writeTemp(t, validYAML))
	if err != nil {
		t.Fatalf("Load valid: %v", err)
	}
	// defaults applied
	if c.Repo.Remote != "origin" || c.Repo.BaseBranch != "main" {
		t.Fatalf("defaults not applied: %+v", c.Repo)
	}
	if c.Trending.Since == "" {
		t.Fatalf("Since default not applied: %+v", c.Trending)
	}
}

func TestLoadMissingRequiredFieldIsConfigInvalid(t *testing.T) {
	// drop model.name -> required
	body := `
model:
  provider: deepseek
  baseURL: https://api.deepseek.com/v1
repo:
  path: /tmp/repo
trending:
  languages: [all]
  since: daily
output:
  dir: digests
`
	_, err := Load(writeTemp(t, body))
	if err == nil {
		t.Fatal("expected error for missing model.name")
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want wrapping ErrInvalid", err)
	}
}

func TestLoadBadYAMLIsConfigInvalid(t *testing.T) {
	_, err := Load(writeTemp(t, "model: [unclosed"))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
}

func TestValidateRejectsBadSince(t *testing.T) {
	c := &Config{
		Model:    ModelConfig{Provider: "deepseek", Name: "m", BaseURL: "https://x.example/v1"},
		Repo:     RepoConfig{Path: "/tmp/repo"},
		Trending: TrendingConfig{Languages: []string{"all"}, Since: "hourly"},
		Output:   OutputConfig{Dir: "digests"},
	}
	if err := c.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for since=hourly, got %v", err)
	}
}

// TestObserveDefaults: the observation store is on by default with the plan's
// default backfill window and candidate cap; an explicit false disables it.
func TestObserveDefaults(t *testing.T) {
	c, err := Load(writeTemp(t, validYAML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.Observe.IsEnabled() {
		t.Fatal("observe should default to enabled")
	}
	if c.Observe.BackfillDays != 30 {
		t.Fatalf("backfillDays = %d, want 30", c.Observe.BackfillDays)
	}
	if c.Observe.MaxCandidates != 300 {
		t.Fatalf("maxCandidates = %d, want 300", c.Observe.MaxCandidates)
	}
}

// TestObserveExplicitDisable: enabled:false is honoured (pointer distinguishes
// unset from false).
func TestObserveExplicitDisable(t *testing.T) {
	body := validYAML + "observe:\n  enabled: false\n"
	c, err := Load(writeTemp(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Observe.IsEnabled() {
		t.Fatal("observe enabled:false should disable the store")
	}
}

// TestObserveStoreDirExpandsEnv: ${VAR} in storeDir is expanded like other paths.
func TestObserveStoreDirExpandsEnv(t *testing.T) {
	t.Setenv("OBSERVE_TEST_DIR", "/custom/obs")
	body := validYAML + "observe:\n  storeDir: ${OBSERVE_TEST_DIR}\n"
	c, err := Load(writeTemp(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Observe.StoreDir != "/custom/obs" {
		t.Fatalf("storeDir = %q, want /custom/obs (env expanded)", c.Observe.StoreDir)
	}
}

// Two valid branch layouts, and anything else is a config error rather than a
// silently ignored typo that would keep the per-period layout.
func TestValidateBranchGrouping(t *testing.T) {
	mk := func(grouping string) *Config {
		return &Config{
			Model:    ModelConfig{Provider: "deepseek", Name: "m", BaseURL: "https://x.example/v1"},
			Repo:     RepoConfig{Path: "/tmp/repo"},
			Trending: TrendingConfig{Languages: []string{"all"}, Since: "daily"},
			Output:   OutputConfig{Dir: "digests", BranchGrouping: grouping},
		}
	}
	for _, ok := range []string{"", BranchGroupingPerPeriod, BranchGroupingPerDate} {
		if err := mk(ok).Validate(); err != nil {
			t.Fatalf("branchGrouping=%q should be valid: %v", ok, err)
		}
	}
	if err := mk("per-day").Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("branchGrouping=per-day should be ErrInvalid, got %v", err)
	}
}
