package summarize

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/model"
)

func TestBuildUserPromptIncludesMetadata(t *testing.T) {
	batch := []digest.RepoSnapshot{
		{FullName: "acme/cache", Language: "go", Stars: 1234, StarsInPeriod: 56, Rank: 3,
			Description: digest.StrPtr("fast cache\nfor services")},
	}
	ctxs := map[string]Context{
		"acme/cache": {Topics: []string{"ai", "llm"}, ReadmeExcerpt: "line one\nline two"},
	}
	got := buildUserPrompt(batch, ctxs)
	if !strings.Contains(got, "REPO=acme/cache\tDESC=fast cache for services") {
		t.Fatalf("missing REPO/DESC line:\n%s", got)
	}
	if !strings.Contains(got, "META=lang=go; stars=1234; starsPeriod=56; rank=3") {
		t.Fatalf("missing meta:\n%s", got)
	}
	if !strings.Contains(got, "TOPICS=ai,llm") {
		t.Fatalf("missing topics line:\n%s", got)
	}
	if strings.Contains(got, "META=lang=go; stars=1234; starsPeriod=56; rank=3; topics=") {
		t.Fatalf("topics must be a separate TOPICS= line, not inside META:\n%s", got)
	}
	if !strings.Contains(got, "README_EXCERPT=line one line two") {
		t.Fatalf("readme should be single line:\n%s", got)
	}
}

func TestSanitizeSummaryStripsMarkup(t *testing.T) {
	raw := "[点这里](https://x.example) <b>加粗</b> 这是一个缓存库 https://y.example"
	got := sanitizeSummary(raw)
	if strings.Contains(got, "http") || strings.Contains(got, "<") || strings.Contains(got, "](") {
		t.Fatalf("markup not stripped: %q", got)
	}
	if !strings.Contains(got, "这是一个缓存库") {
		t.Fatalf("content lost: %q", got)
	}
}

func TestParseAndValidateRejectsLongAndRepeatsDescription(t *testing.T) {
	batch := []digest.RepoSnapshot{{FullName: "acme/x", Description: digest.StrPtr("some description")}}
	long := strings.Repeat("中", 81)
	raw := fmt.Sprintf(`{"summaries":[{"fullName":"acme/x","summaryZh":%q}]}`, long)
	if _, invalid, err := parseAndValidate(raw, batch); err != nil || len(invalid) != 1 {
		t.Fatalf("long summary should be invalid: err=%v invalid=%v", err, invalid)
	}

	raw = `{"summaries":[{"fullName":"acme/x","summaryZh":"some description"}]}`
	if _, invalid, err := parseAndValidate(raw, batch); err != nil || len(invalid) != 1 {
		t.Fatalf("repeat-description should be invalid: err=%v invalid=%v", err, invalid)
	}
}

// A repo whose name/description is almost entirely English brand and API names
// still deserves a valid Chinese summary. The old "Han ratio >= 40%" gate
// rejected these even when the summary was perfectly good Chinese, so a whole
// batch degraded to "original" for no real reason. English-heavy must not be a
// reason to reject, as long as the summary is actually Chinese.
func TestParseAndValidateAcceptsLatinHeavyChineseSummary(t *testing.T) {
	batch := []digest.RepoSnapshot{{
		FullName:    "Mag1cFall/AIStudio2API",
		Description: digest.StrPtr("将AI Studio Playground和Build反代成API | AI Studio to OpenAI, Anthropic & Gemini Compatible API"),
	}}
	// Genuine Chinese summary, but dominated by Latin brand names (Han ratio ~0.14).
	summary := "把 AI Studio Playground 与 Build 反代为 OpenAI、Anthropic、Gemini 兼容 API 的服务。"
	raw := fmt.Sprintf(`{"summaries":[{"fullName":"Mag1cFall/AIStudio2API","summaryZh":%q}]}`, summary)
	got, invalid, err := parseAndValidate(raw, batch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(invalid) != 0 {
		t.Fatalf("latin-heavy Chinese summary should be valid, got invalid=%v", invalid)
	}
	if got["Mag1cFall/AIStudio2API"] != summary {
		t.Fatalf("summary = %q, want %q", got["Mag1cFall/AIStudio2API"], summary)
	}
}

// Pure-English output (model echoed the description instead of translating)
// must still be rejected: it contains no Han characters.
func TestParseAndValidateRejectsPureEnglishSummary(t *testing.T) {
	batch := []digest.RepoSnapshot{{FullName: "acme/cache", Description: digest.StrPtr("A fast cache for services")}}
	raw := `{"summaries":[{"fullName":"acme/cache","summaryZh":"A cache library for high concurrency services"}]}`
	if _, invalid, err := parseAndValidate(raw, batch); err != nil || len(invalid) != 1 {
		t.Fatalf("pure-English summary should be invalid: err=%v invalid=%v", err, invalid)
	}
}

type repairProvider struct {
	mu    sync.Mutex
	calls int
	users []string
}

type summaryItem struct {
	FullName  string `json:"fullName"`
	SummaryZh string `json:"summaryZh"`
}

func (p *repairProvider) Name() string { return "repair" }
func (p *repairProvider) Complete(_ context.Context, _, user string) (model.Result, error) {
	p.mu.Lock()
	p.calls++
	p.users = append(p.users, user)
	call := p.calls
	p.mu.Unlock()

	names := repoLineRe.FindAllStringSubmatch(user, -1)
	items := make([]summaryItem, 0, len(names))
	for i, m := range names {
		// First attempt omits the last repo, forcing a repair pass for just it.
		if call == 1 && i == len(names)-1 {
			continue
		}
		items = append(items, summaryItem{FullName: m[1], SummaryZh: "这是用于测试的中文摘要，说明该仓库的用途。"})
	}
	b, _ := json.Marshal(map[string]any{"summaries": items})
	return model.Result{Text: string(b)}, nil
}

var repoLineRe = regexp.MustCompile(`(?m)^REPO=(\S+)\tDESC=`)

func TestRepairOnlyRequestsMissingRepos(t *testing.T) {
	p := &repairProvider{}
	s := New(p, WithMaxRetries(1), WithBatchSize(10))
	out := s.Summarize(context.Background(), snaps(3))
	for _, d := range out {
		if d.SummarySource != "model" {
			t.Fatalf("%s source = %s, want model", d.FullName, d.SummarySource)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls != 2 {
		t.Fatalf("calls = %d, want 2 (initial + repair)", p.calls)
	}
	if strings.Count(p.users[1], "REPO=") != 1 {
		t.Fatalf("repair prompt should contain only the missing repo:\n%s", p.users[1])
	}
}

func TestFormattingFakeOutputPassesValidation(t *testing.T) {
	// A guard that offline Fake output remains a valid summary under the new
	// validator; otherwise every offline run would silently degrade.
	s := New(model.NewFake())
	for _, d := range s.Summarize(context.Background(), snaps(2)) {
		if d.SummarySource != "model" {
			t.Fatalf("%s degraded: %q", d.FullName, d.SummaryZh)
		}
	}
}
