package config

import (
	"errors"
	"strings"
	"testing"
)

// baseConfig is a minimal valid config body; tests append a language block.
const baseConfig = `
model:
  provider: deepseek
  name: deepseek-chat
  baseURL: https://api.deepseek.com/v1
  apiKey: test-key
repo:
  path: /tmp/repo
trending:
  languages: [all, go]
  since: daily
output:
  dir: digests
`

func TestLanguageDefaultIsZh(t *testing.T) {
	c, err := Load(writeTemp(t, baseConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Language.OutputLanguage != "zh" {
		t.Fatalf("default outputLanguage = %q, want zh", c.Language.OutputLanguage)
	}
	if got := c.EffectiveOutputLanguage(); got != "zh" {
		t.Fatalf("EffectiveOutputLanguage = %q, want zh", got)
	}
}

func TestLanguageExplicitEn(t *testing.T) {
	body := baseConfig + `
language:
  outputLanguage: en
`
	c, err := Load(writeTemp(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Language.OutputLanguage != "en" {
		t.Fatalf("outputLanguage = %q, want en", c.Language.OutputLanguage)
	}
}

func TestLanguageRegionNormalized(t *testing.T) {
	body := baseConfig + `
language:
  outputLanguage: en-US
`
	c, err := Load(writeTemp(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := c.EffectiveOutputLanguage(); got != "en" {
		t.Fatalf("EffectiveOutputLanguage = %q, want en", got)
	}
}

func TestLanguageUnsupportedRejected(t *testing.T) {
	for _, id := range []string{"fr", "de-DE", "jp"} {
		body := baseConfig + "\nlanguage:\n  outputLanguage: " + id + "\n"
		_, err := Load(writeTemp(t, body))
		if err == nil {
			t.Errorf("Load(%q) should fail (unsupported)", id)
			continue
		}
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("error for %q = %v, want wrapping ErrInvalid", id, err)
		}
		if !strings.Contains(err.Error(), "supported") {
			t.Errorf("error for %q should list supported set: %v", id, err)
		}
	}
}

func TestLanguageInvalidTagRejected(t *testing.T) {
	body := baseConfig + "\nlanguage:\n  outputLanguage: zzzzzz_1\n"
	_, err := Load(writeTemp(t, body))
	if err == nil {
		t.Fatal("Load should fail for invalid BCP47 tag")
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want wrapping ErrInvalid", err)
	}
}

func TestLanguageEmptyAllowed(t *testing.T) {
	// An explicit empty value is valid and resolves to the default.
	body := baseConfig + `
language:
  outputLanguage: ""
`
	c, err := Load(writeTemp(t, body))
	if err != nil {
		t.Fatalf("Load with empty language: %v", err)
	}
	if got := c.EffectiveOutputLanguage(); got != "zh" {
		t.Fatalf("EffectiveOutputLanguage = %q, want zh", got)
	}
}
