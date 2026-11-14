package config

import (
	"errors"
	"testing"
)

// baseModelYAML is the minimal valid model block shared by the fallback cases.
const baseModelYAML = `
model:
  provider: deepseek
  name: deepseek-chat
  baseURL: https://api.deepseek.com/v1
  apiKey: test-key
repo:
  path: /tmp/repo
trending:
  languages: [all]
  since: daily
output:
  dir: digests
`

func loadModel(t *testing.T, modelBlock string) (*Config, error) {
	t.Helper()
	body := modelBlock + `
repo:
  path: /tmp/repo
trending:
  languages: [all]
  since: daily
output:
  dir: digests
`
	return Load(writeTemp(t, body))
}

// TestFallbacksAbsentIsNilChain: no fallbacks key -> nil chain, behaviour
// unchanged (zero regression).
func TestFallbacksAbsentIsNilChain(t *testing.T) {
	c, err := Load(writeTemp(t, baseModelYAML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Model.Fallbacks != nil {
		t.Fatalf("Fallbacks = %+v, want nil", c.Model.Fallbacks)
	}
	if len(c.Model.Fallbacks) != 0 {
		t.Fatalf("len(Fallbacks) = %d, want 0", len(c.Model.Fallbacks))
	}
}

// TestFallbacksSingleEntry: one well-formed link parses with all fields,
// including the *float32 temperature pointer.
func TestFallbacksSingleEntry(t *testing.T) {
	body := `
model:
  provider: deepseek
  name: deepseek-chat
  baseURL: https://api.deepseek.com/v1
  apiKey: test-key
  fallbacks:
    - provider: openai
      name: gpt-4o-mini
      baseURL: https://api.openai.com/v1
      apiKey: back-up-key
      temperature: 0.3
      maxTokens: 2048
repo:
  path: /tmp/repo
trending:
  languages: [all]
  since: daily
output:
  dir: digests
`
	c, err := Load(writeTemp(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Model.Fallbacks) != 1 {
		t.Fatalf("len(Fallbacks) = %d, want 1", len(c.Model.Fallbacks))
	}
	fb := c.Model.Fallbacks[0]
	if fb.Provider != "openai" || fb.Name != "gpt-4o-mini" {
		t.Fatalf("provider/name = %q/%q, want openai/gpt-4o-mini", fb.Provider, fb.Name)
	}
	if fb.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("BaseURL = %q", fb.BaseURL)
	}
	if fb.APIKey != "back-up-key" {
		t.Fatalf("APIKey = %q", fb.APIKey)
	}
	if fb.Temperature == nil || *fb.Temperature != 0.3 {
		t.Fatalf("Temperature = %v, want 0.3", fb.Temperature)
	}
	if fb.MaxTokens != 2048 {
		t.Fatalf("MaxTokens = %d, want 2048", fb.MaxTokens)
	}
}

// TestFallbacksMultiEntryOrderPreserved: order is the attempt order and must be
// preserved exactly.
func TestFallbacksMultiEntryOrderPreserved(t *testing.T) {
	body := `
model:
  provider: deepseek
  name: deepseek-chat
  baseURL: https://api.deepseek.com/v1
  apiKey: test-key
  fallbacks:
    - provider: openai
      name: gpt-4o-mini
      baseURL: https://api.openai.com/v1
    - provider: anthropic
      name: claude-3-5-haiku
    - provider: qwen
      name: qwen-plus
      apiKey: qwen-key
repo:
  path: /tmp/repo
trending:
  languages: [all]
  since: daily
output:
  dir: digests
`
	c, err := Load(writeTemp(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Model.Fallbacks) != 3 {
		t.Fatalf("len(Fallbacks) = %d, want 3", len(c.Model.Fallbacks))
	}
	want := []struct{ provider, name string }{
		{"openai", "gpt-4o-mini"},
		{"anthropic", "claude-3-5-haiku"},
		{"qwen", "qwen-plus"},
	}
	for i, w := range want {
		got := c.Model.Fallbacks[i]
		if got.Provider != w.provider || got.Name != w.name {
			t.Fatalf("fallbacks[%d] = %q/%q, want %q/%q", i, got.Provider, got.Name, w.provider, w.name)
		}
	}
	// Absent optional fields stay zero / nil.
	if c.Model.Fallbacks[0].Temperature != nil {
		t.Fatalf("fallbacks[0].Temperature = %v, want nil", c.Model.Fallbacks[0].Temperature)
	}
}

// TestFallbackMissingProviderInvalid: provider is required per link.
func TestFallbackMissingProviderInvalid(t *testing.T) {
	body := `
model:
  provider: deepseek
  name: deepseek-chat
  baseURL: https://api.deepseek.com/v1
  apiKey: test-key
  fallbacks:
    - name: gpt-4o-mini
repo:
  path: /tmp/repo
trending:
  languages: [all]
  since: daily
output:
  dir: digests
`
	_, err := loadErr(t, body)
	if err == nil {
		t.Fatal("expected error for fallback missing provider")
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
}

// TestFallbackMissingNameInvalid: name is required per link.
func TestFallbackMissingNameInvalid(t *testing.T) {
	body := `
model:
  provider: deepseek
  name: deepseek-chat
  baseURL: https://api.deepseek.com/v1
  apiKey: test-key
  fallbacks:
    - provider: openai
repo:
  path: /tmp/repo
trending:
  languages: [all]
  since: daily
output:
  dir: digests
`
	_, err := loadErr(t, body)
	if err == nil {
		t.Fatal("expected error for fallback missing name")
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
}

// TestFallbackBadBaseURLInvalid: baseURL is optional but must be a valid URL
// when present.
func TestFallbackBadBaseURLInvalid(t *testing.T) {
	body := `
model:
  provider: deepseek
  name: deepseek-chat
  baseURL: https://api.deepseek.com/v1
  apiKey: test-key
  fallbacks:
    - provider: openai
      name: gpt-4o-mini
      baseURL: "not a url"
repo:
  path: /tmp/repo
trending:
  languages: [all]
  since: daily
output:
  dir: digests
`
	_, err := loadErr(t, body)
	if err == nil {
		t.Fatal("expected error for invalid fallback baseURL")
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
}

// TestFallbackEmptyBaseURLOK: baseURL may be omitted (empty) per link.
func TestFallbackEmptyBaseURLOK(t *testing.T) {
	body := `
model:
  provider: deepseek
  name: deepseek-chat
  baseURL: https://api.deepseek.com/v1
  apiKey: test-key
  fallbacks:
    - provider: qwen
      name: qwen-plus
repo:
  path: /tmp/repo
trending:
  languages: [all]
  since: daily
output:
  dir: digests
`
	c, err := Load(writeTemp(t, body))
	if err != nil {
		t.Fatalf("Load with empty fallback baseURL should be valid: %v", err)
	}
	if len(c.Model.Fallbacks) != 1 || c.Model.Fallbacks[0].BaseURL != "" {
		t.Fatalf("unexpected fallback: %+v", c.Model.Fallbacks)
	}
}

// loadErr is a small helper that keeps the invalid cases readable.
func loadErr(t *testing.T, body string) (*Config, error) {
	t.Helper()
	return Load(writeTemp(t, body))
}
