package main

import (
	"testing"

	"github.com/morehao/trendradar/internal/config"
	"github.com/morehao/trendradar/internal/model"
)

// With no fallbacks, selectProvider must return the bare primary provider
// (chain length stays 1) — wrapping it in an empty chain would turn every call
// into gllm.ErrConfigInvalid. Zero behaviour change for existing configs.
func TestSelectProviderNoFallbacksReturnsPrimary(t *testing.T) {
	cfg := &config.Config{Model: config.ModelConfig{
		Provider: "deepseek",
		Name:     "deepseek-chat",
		BaseURL:  "https://api.deepseek.com/v1",
		APIKey:   "sk-test",
	}}
	p, err := selectProvider(cfg)
	if err != nil {
		t.Fatalf("selectProvider: %v", err)
	}
	if _, ok := p.(*model.FallbackProvider); ok {
		t.Fatalf("no fallbacks must not be wrapped in a FallbackProvider, got %T", p)
	}
	if p == nil {
		t.Fatalf("nil provider")
	}
}

// With fallbacks, the primary plus each entry are packed into a FallbackProvider
// of length len(Fallbacks)+1; the first successful member records its index.
func TestSelectProviderBuildsFallbackChain(t *testing.T) {
	cfg := &config.Config{Model: config.ModelConfig{
		Provider: "deepseek",
		Name:     "deepseek-chat",
		BaseURL:  "https://api.deepseek.com/v1",
		APIKey:   "sk-test",
		Fallbacks: []config.ModelEntry{
			{Provider: "openai", Name: "gpt-4o-mini", BaseURL: "https://api.openai.com/v1", APIKey: "sk-b1"},
			{Provider: "openai", Name: "gpt-4o", BaseURL: "https://api.openai.com/v1", APIKey: "sk-b2"},
		},
	}}
	p, err := selectProvider(cfg)
	if err != nil {
		t.Fatalf("selectProvider: %v", err)
	}
	fp, ok := p.(*model.FallbackProvider)
	if !ok {
		t.Fatalf("with fallbacks want *FallbackProvider, got %T", p)
	}
	// No member has succeeded yet, so UsedIndex is -1 — this only proves the
	// object is the chain type; the length invariant is guarded by construction
	// (primary + one per fallback) and exercised by internal/model tests.
	if got := fp.UsedIndex(); got != -1 {
		t.Fatalf("UsedIndex before any call = %d, want -1", got)
	}
	if fp.Name() != "fallback:"+mustName(t, cfg) {
		t.Fatalf("chain Name() = %q, want primary prefixed", fp.Name())
	}
}

// fake provider configs (provider empty/`fake`) bypass the chain entirely.
func TestSelectProviderFake(t *testing.T) {
	p, err := selectProvider(&config.Config{})
	if err != nil {
		t.Fatalf("selectProvider: %v", err)
	}
	if _, ok := p.(*model.Fake); !ok {
		t.Fatalf("fake config want *model.Fake, got %T", p)
	}
}

// mustName builds the primary provider directly to derive its Name() prefix.
func mustName(t *testing.T, cfg *config.Config) string {
	t.Helper()
	prim, err := model.NewGLLM(model.GLLMOptions{
		Provider:  cfg.Model.Provider,
		ModelName: cfg.Model.Name,
		BaseURL:   cfg.Model.BaseURL,
		APIKey:    cfg.Model.APIKey,
	})
	if err != nil {
		t.Fatalf("NewGLLM primary: %v", err)
	}
	return prim.Name()
}
