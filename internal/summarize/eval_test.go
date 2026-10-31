//go:build eval

package summarize

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/enrich"
	"github.com/morehao/trendradar/internal/envfile"
	"github.com/morehao/trendradar/internal/model"
)

// TestEvalSummaries is a manual, live-model evaluation harness. It is excluded
// from normal CI by the `eval` build tag. Put keys in a git-ignored .env and run:
//
//	make test-eval
//	# or: go test -tags eval ./internal/summarize -run TestEvalSummaries -v
//
// It reads TRENDRADAR_EVAL_PROVIDER/MODEL/BASE_URL/API_KEY (after loading the
// nearest .env), reports automatic metrics, and can dump summaries for review.
func TestEvalSummaries(t *testing.T) {
	_, _ = envfile.ApplyDefault(".")
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
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("TRENDRADAR_EVAL_ENRICH")))
	if mode == "" {
		mode = string(enrich.ModeNone)
	}
	em, err := enrich.ParseMode(mode)
	if err != nil {
		t.Fatalf("parse enrich mode: %v", err)
	}

	ctx := context.Background()
	s := New(provider, WithCache(NewCache()))
	var out []digest.RepoDigest
	var er enrich.Report
	if em == enrich.ModeNone {
		out = s.Summarize(ctx, repos)
	} else {
		readmeChars := 0
		if v := os.Getenv("TRENDRADAR_EVAL_README_CHARS"); v != "" {
			readmeChars, _ = strconv.Atoi(v)
		}
		budgetSeconds, _ := strconv.Atoi(os.Getenv("TRENDRADAR_EVAL_ENRICH_BUDGET_SECONDS"))
		concurrency, _ := strconv.Atoi(os.Getenv("TRENDRADAR_EVAL_ENRICH_CONCURRENCY"))
		enr := enrich.New(enrich.Options{
			Mode:        em,
			ReadmeChars: readmeChars,
			Concurrency: concurrency,
			Budget:      time.Duration(budgetSeconds) * time.Second,
			HTMLBase:    os.Getenv("TRENDRADAR_EVAL_HTML_BASE"),
			RawBase:     os.Getenv("TRENDRADAR_EVAL_RAW_BASE"),
		}, enrich.NewCache())
		// Only enrich repos whose summary is not cached, mirroring app.Run.
		pending := s.Pending(repos)
		ctxs, report := enr.Enrich(ctx, pending)
		er = report
		byRepo := make(map[string]Context, len(ctxs))
		for name, c := range ctxs {
			byRepo[name] = Context{Topics: c.Topics, ReadmeExcerpt: c.ReadmeExcerpt}
		}
		out = s.SummarizeWithContext(ctx, repos, byRepo)
	}
	stats := s.Stats()

	type report struct {
		Total             int    `json:"total"`
		Model             int    `json:"model"`
		Degraded          int    `json:"degraded"`
		LengthOK          int    `json:"lengthOk"`
		ChineseOK         int    `json:"chineseOk"`
		RepeatDesc        int    `json:"repeatDesc"`
		EmptyPhrase       int    `json:"emptyPhrase"`
		PromptTokens      int    `json:"promptTokens"`
		CompletionTokens  int    `json:"completionTokens"`
		TotalTokens       int    `json:"totalTokens"`
		UsageKnown        bool   `json:"usageKnown"`
		EnrichMode        string `json:"enrichMode"`
		EnrichRequested   int    `json:"enrichRequested"`
		EnrichFetched     int    `json:"enrichFetched"`
		EnrichFailed      int    `json:"enrichFailed"`
		EnrichRateLimited int    `json:"enrichRateLimited"`
	}
	rep := report{
		Total: len(out), PromptTokens: stats.Usage.PromptTokens, CompletionTokens: stats.Usage.CompletionTokens,
		TotalTokens: stats.Usage.TotalTokens, UsageKnown: stats.Usage.Known,
		EnrichMode: string(em), EnrichRequested: er.Requested, EnrichFetched: er.Fetched,
		EnrichFailed: er.Failed, EnrichRateLimited: er.RateLimited,
	}
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
