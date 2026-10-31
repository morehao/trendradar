//go:build eval

package summarize

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/model"
)

// TestEvalSummaries is a manual, live-model evaluation harness. It is excluded
// from normal CI by the `eval` build tag. Run it with:
//
//	TRENDRADAR_EVAL_PROVIDER=deepseek \
//	TRENDRADAR_EVAL_MODEL=deepseek-chat \
//	TRENDRADAR_EVAL_BASE_URL=https://api.deepseek.com/v1 \
//	TRENDRADAR_EVAL_API_KEY=... \
//	go test -tags eval ./internal/summarize -run TestEvalSummaries -v
//
// Quality scores (具体性 / 事实错误) still need a human reviewer; this harness
// reports the automatic metrics and can dump the summaries for review.
func TestEvalSummaries(t *testing.T) {
	input := os.Getenv("TRENDRADAR_EVAL_INPUT")
	if input == "" {
		input = "testdata/summary-eval/repos.json"
	}
	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatalf("read eval input: %v", err)
	}
	var repos []digest.RepoSnapshot
	if err := json.Unmarshal(data, &repos); err != nil {
		t.Fatalf("parse eval input: %v", err)
	}

	provider, err := evalProvider()
	if err != nil {
		t.Fatalf("build provider: %v", err)
	}
	s := New(provider, WithCache(NewCache()))
	out := s.Summarize(context.Background(), repos)
	stats := s.Stats()

	type report struct {
		Total            int  `json:"total"`
		Model            int  `json:"model"`
		Degraded         int  `json:"degraded"`
		LengthOK         int  `json:"lengthOk"`
		ChineseOK        int  `json:"chineseOk"`
		RepeatDesc       int  `json:"repeatDesc"`
		EmptyPhrase      int  `json:"emptyPhrase"`
		PromptTokens     int  `json:"promptTokens"`
		CompletionTokens int  `json:"completionTokens"`
		TotalTokens      int  `json:"totalTokens"`
		UsageKnown       bool `json:"usageKnown"`
	}
	rep := report{Total: len(out), PromptTokens: stats.Usage.PromptTokens, CompletionTokens: stats.Usage.CompletionTokens, TotalTokens: stats.Usage.TotalTokens, UsageKnown: stats.Usage.Known}
	for _, d := range out {
		if d.SummarySource == "model" {
			rep.Model++
		} else {
			rep.Degraded++
		}
		runes := utf8.RuneCountInString(d.SummaryZh)
		if runes <= 60 {
			rep.LengthOK++
		}
		if utf8.RuneCountInString(d.SummaryZh) > 0 && float64(countHan(d.SummaryZh))/float64(utf8.RuneCountInString(d.SummaryZh)) >= 0.4 {
			rep.ChineseOK++
		}
		if strings.TrimSpace(d.SummaryZh) == strings.TrimSpace(digest.Str(d.Description)) {
			rep.RepeatDesc++
		}
		for _, bad := range []string{"这是一个", "该项目", "旨在"} {
			if strings.HasPrefix(strings.TrimSpace(d.SummaryZh), bad) {
				rep.EmptyPhrase++
				break
			}
		}
	}

	b, _ := json.MarshalIndent(struct {
		Input  string              `json:"input"`
		Report report              `json:"report"`
		Repos  []digest.RepoDigest `json:"repos"`
	}{input, rep, out}, "", "  ")
	fmt.Println(string(b))

	if outPath := os.Getenv("TRENDRADAR_EVAL_OUT"); outPath != "" {
		if err := os.WriteFile(outPath, append(b, '\n'), 0o644); err != nil {
			t.Fatalf("write eval output: %v", err)
		}
	}
}

func evalProvider() (model.Provider, error) {
	name := os.Getenv("TRENDRADAR_EVAL_PROVIDER")
	if name == "" || name == "fake" {
		return model.NewFake(), nil
	}
	return model.NewGLLM(model.GLLMOptions{
		Provider:  name,
		ModelName: os.Getenv("TRENDRADAR_EVAL_MODEL"),
		BaseURL:   os.Getenv("TRENDRADAR_EVAL_BASE_URL"),
		APIKey:    os.Getenv("TRENDRADAR_EVAL_API_KEY"),
	})
}
