package releasewatch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/morehao/trendradar/internal/fetch"
	"github.com/morehao/trendradar/internal/i18n"
	"github.com/morehao/trendradar/internal/model"
	"github.com/morehao/trendradar/internal/summarize"
)

// SummaryResult is one repo's weekly summary. Source is "model" or "original".
type SummaryResult struct {
	Summary   string
	Source    string
	Truncated bool
}

// candidate is one release/tag to include in the week.
type candidate struct {
	repoFull string
	repoURL  string
	rel      fetch.Release
	kind     string // release | tag
}

func (c candidate) tagName() string {
	if c.rel.TagName != "" {
		return c.rel.TagName
	}
	return c.rel.Name
}

// cacheKey covers one version: its tag and a hash of its notes, so an edited
// body re-summarizes while an unchanged version is served from cache. The key is
// per (repo, tag) — the incremental unit — not per week, so adding one version to
// a week only summarizes that one and reuses the rest.
func (s *Summarizer) cacheKey(c candidate) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00", c.tagName(), c.rel.Body)
	return c.tagName() + "|" + hex.EncodeToString(h.Sum(nil)[:8])
}

// Summarizer writes weekly release summaries with a persistent cache and
// per-repo degradation (a failure never aborts the run).
type Summarizer struct {
	chat     model.Provider
	cache    *summarize.Cache
	maxNotes int
	maxChars int
	lang     i18n.OutputLanguage

	mu    sync.Mutex
	usage model.Usage
	calls int
	trunc int
}

// Stats returns accumulated token usage, real provider call count and how many
// summaries had to be truncated.
func (s *Summarizer) Stats() (model.Usage, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usage, s.calls, s.trunc
}

// NewSummarizer builds a weekly release summarizer. cache may be nil. langId is
// the output language id (empty means zh); it drives the prompt and degrade text.
func NewSummarizer(chat model.Provider, cache *summarize.Cache, maxNotes, maxChars int, langId string) *Summarizer {
	if maxChars <= 0 {
		maxChars = DefaultSummaryMaxChars
	}
	lang, err := i18n.Resolve(langId)
	if err != nil {
		lang = i18n.MustResolve(i18n.DefaultLanguage)
	}
	return &Summarizer{chat: chat, cache: cache, maxNotes: maxNotes, maxChars: maxChars, lang: lang}
}

// SummarizeCandidate produces one Chinese summary for a single version. Each
// version is summarized independently so the document carries a per-release
// summary (the incremental unit), and the cache is keyed per (repo, tag) so only
// newly published versions cost a model call. A cache hit costs nothing; a model
// failure degrades to the original notes instead of dropping the version.
func (s *Summarizer) SummarizeCandidate(ctx context.Context, repo string, c candidate) SummaryResult {
	key := s.cacheKey(c)
	if s.cache != nil {
		if sum, ok := s.cache.Get(repo, key); ok {
			return SummaryResult{Summary: sum, Source: "model"}
		}
	}
	if s.chat != nil {
		res, err := s.chat.Complete(ctx, s.lang.ReleaseSystem, buildItemPrompt(s.lang, repo, c, s.maxNotes, s.maxChars))
		if err == nil {
			s.mu.Lock()
			s.calls++
			if res.Usage.Known {
				s.usage.PromptTokens += res.Usage.PromptTokens
				s.usage.CompletionTokens += res.Usage.CompletionTokens
				s.usage.TotalTokens += res.Usage.TotalTokens
				s.usage.CachedTokens += res.Usage.CachedTokens
				s.usage.Known = true
			}
			s.mu.Unlock()
			if sum, ok := parseWeeklySummary(res.Text); ok {
				sum, truncated := s.cap(sum)
				if truncated {
					s.mu.Lock()
					s.trunc++
					s.mu.Unlock()
				}
				if s.cache != nil {
					s.cache.Put(repo, key, sum)
				}
				return SummaryResult{Summary: sum, Source: "model", Truncated: truncated}
			}
		}
	}
	sum, truncated := s.cap(degradeOne(s.lang, c))
	return SummaryResult{Summary: sum, Source: "original", Truncated: truncated}
}

// SummarizeRepo summarizes every candidate independently and returns one result
// per candidate, in the same order. It is a thin fan-out over SummarizeCandidate
// so callers (and older tests) keep a single entry point.
func (s *Summarizer) SummarizeRepo(ctx context.Context, repo, weekKey string, cands []candidate) []SummaryResult {
	out := make([]SummaryResult, 0, len(cands))
	for _, c := range cands {
		out = append(out, s.SummarizeCandidate(ctx, repo, c))
	}
	return out
}

// cap enforces summaryMaxChars by truncating with an ellipsis rather than
// rejecting the whole summary: a slightly long summary is still useful, a
// dropped one is not.
func (s *Summarizer) cap(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	limit := s.maxChars
	if limit <= 0 {
		limit = DefaultSummaryMaxChars
	}
	r := []rune(text)
	if len(r) <= limit {
		return text, false
	}
	return string(r[:limit-1]) + "…", true
}

// parseWeeklySummary accepts the documented JSON shape and, as a fallback, a
// plain-text answer (some providers drop the JSON instruction).
func parseWeeklySummary(raw string) (string, bool) {
	text := stripFence(strings.TrimSpace(raw))
	if text == "" {
		return "", false
	}
	var resp struct {
		Repo    string `json:"repo"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err == nil {
		sum := sanitize(resp.Summary)
		return sum, sum != ""
	}
	// Not JSON: treat a short prose answer as the summary, anything else
	// (an error blob, a long code block) as a failure so we degrade instead.
	sum := sanitize(text)
	if sum == "" || utf8.RuneCountInString(sum) > 4000 {
		return "", false
	}
	return sum, true
}

var (
	reMDLink = regexp.MustCompile(`!?\[[^\]]*\]\([^)]*\)`)
	reHTML   = regexp.MustCompile(`<[^>]*>`)
	reURL    = regexp.MustCompile(`https?://\S+`)
	// leadHash strips a rogue ATX heading marker: the release *document* splits
	// sections on `## ` lines, so model output must never introduce a heading
	// that could be mistaken for a section boundary. The prompt forbids `#`
	// anyway; this is the defensive net.
	leadHash = regexp.MustCompile(`^\s{0,3}#{1,6}\s*`)
)

// sanitize keeps the summary safe while preserving the light Markdown structure
// the model is asked to produce (bold labels + `- ` bullets). It still treats
// the text as data: Markdown links, raw HTML and bare URLs are removed, and the
// text is never interpreted as instructions.
//
// Unlike the previous version it does NOT collapse every whitespace run into a
// single space — that flattened structured output back into one dense paragraph,
// which is exactly the readability problem this change fixes. Line breaks are
// kept; only pathological runs of blank lines are squeezed.
func sanitize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = reMDLink.ReplaceAllString(s, "")
	s = reHTML.ReplaceAllString(s, "")
	s = reURL.ReplaceAllString(s, "")
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\n':
			return '\n'
		case '\t':
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, line := range lines {
		line = strings.TrimRight(line, " ")
		line = leadHash.ReplaceAllString(line, "")
		// Normalize common bullet glyphs to a Markdown "- " so the renderer
		// treats them consistently.
		line = strings.TrimLeft(line, " ")
		switch {
		case line == "•" || line == "·":
			line = "- "
		case strings.HasPrefix(line, "• "):
			line = "- " + strings.TrimPrefix(line, "• ")
		case strings.HasPrefix(line, "· "):
			line = "- " + strings.TrimPrefix(line, "· ")
		}
		if strings.TrimSpace(line) == "" {
			blank++
			if blank > 1 {
				continue
			}
			out = append(out, "")
			continue
		}
		blank = 0
		out = append(out, line)
	}
	// Trim leading/trailing blank lines.
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

// degradeOne is the no-model fallback for a single version: keep its own notes
// so the version is not silently lost, trimmed to the configured cap later.
// Formatted like degradeSummary (bold tag + its own lines) so a per-version
// fallback reads the same as the week fallback.
func degradeOne(lang i18n.OutputLanguage, c candidate) string {
	text := sanitize(c.rel.Body)
	if text == "" {
		return noChangelogOne(lang, c.tagName())
	}
	return "**" + c.tagName() + "**\n" + text
}

// degradeSummary is the no-model fallback: it keeps every version's own notes
// so the week is not silently lost, then trims to the configured budget. Notes
// are kept on their own lines so the document stays readable when the model is
// unavailable. Kept for callers that still summarize a batch at once.
func degradeSummary(cands []candidate) string {
	parts := make([]string, 0, len(cands))
	for _, c := range cands {
		text := sanitize(c.rel.Body)
		if text == "" {
			continue
		}
		parts = append(parts, "**"+c.tagName()+"**\n"+text)
	}
	if len(parts) == 0 {
		return fmt.Sprintf("本周 %d 个版本（未获取到变更说明）", len(cands))
	}
	return "本周 " + fmt.Sprint(len(cands)) + " 个版本。" + strings.Join(parts, "\n\n")
}

// hanRatio is used by tests/diagnostics if needed.
func hanRatio(s string) float64 {
	total := utf8.RuneCountInString(s)
	if total == 0 {
		return 0
	}
	n := 0
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			n++
		}
	}
	return float64(n) / float64(total)
}

// stripFence removes a surrounding ```json ... ``` code fence if present.
func stripFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimPrefix(s, "json")
	s = strings.TrimPrefix(s, "\n")
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
}
