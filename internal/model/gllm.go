package model

import (
	"context"

	"github.com/cloudwego/eino/schema"
	"github.com/morehao/golib/gllm"
	_ "github.com/morehao/golib/gllm/driver/openai" // register the "openai" driver
)

// GLLMOptions configures an OpenAI-compatible model via golib/gllm.
type GLLMOptions struct {
	Provider      string // logical provider name, e.g. "deepseek"
	ModelName     string // model key, e.g. "deepseek-chat"
	BaseURL       string // full endpoint, e.g. https://api.deepseek.com/v1
	APIKey        string
	Temperature   *float32
	MaxTokens     int
	AllowDegraded bool
}

type gllmProvider struct {
	m *gllm.Model
}

// NewGLLM builds a Provider backed by golib/gllm + the openai driver.
func NewGLLM(opts GLLMOptions) (Provider, error) {
	provider := opts.Provider
	if provider == "" {
		provider = "default"
	}
	modelCfg := gllm.ModelConfig{
		Provider: provider,
		Extra:    map[string]any{"response_format": map[string]any{"type": "json_object"}},
	}
	if opts.Temperature != nil {
		modelCfg.Temperature = opts.Temperature
	}
	if opts.MaxTokens > 0 {
		mt := opts.MaxTokens
		modelCfg.MaxTokens = &mt
	}
	cfg := gllm.Config{
		Providers: map[string]gllm.Provider{
			provider: {Type: "openai", BaseURL: opts.BaseURL, APIKey: opts.APIKey},
		},
		Models:        map[string]gllm.ModelConfig{opts.ModelName: modelCfg},
		AllowDegraded: opts.AllowDegraded,
	}
	m, err := gllm.New(context.Background(), cfg, opts.ModelName)
	if err != nil {
		return nil, err
	}
	return &gllmProvider{m: m}, nil
}

func (p *gllmProvider) Name() string { return "gllm:" + p.m.ProviderType }

func (p *gllmProvider) Complete(ctx context.Context, system, user string) (Result, error) {
	out, err := p.m.ChatModel.Generate(ctx, []*schema.Message{
		schema.SystemMessage(system),
		schema.UserMessage(user),
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Text: out.Content, Degraded: p.m.Degraded}, nil
}
