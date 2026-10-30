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
	if c.Trending.PerLanguage != 20 {
		t.Fatalf("PerLanguage default = %d, want 20", c.Trending.PerLanguage)
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
