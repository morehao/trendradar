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
	"github.com/morehao/trendradar/internal/model"
	"github.com/morehao/trendradar/internal/summarize"
)

// SummaryResult is one repo's weekly summary. Source is "model" or "original".
type SummaryResult struct {
	SummaryZh string
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

// cacheKey covers the whole week: the week key plus a hash of every tag and its
// notes, so an edited release body or a late-added tag re-summarizes while an
// unchanged week is served from cache.
func (s *Summarizer) cacheKey(weekKey string, cands []candidate) string {
	h := sha256.New()
	for _, c := range cands {
		fmt.Fprintf(h, "%s\x00%s\x00", c.tagName(), c.rel.Body)
	}
	return weekKey + "|" + hex.EncodeToString(h.Sum(nil)[:8])
}

// Summarizer writes weekly release summaries with a persistent cache and
// per-repo degradation (a failure never aborts the run).
type Summarizer struct {
	chat     model.Provider
	cache    *summarize.Cache
	maxNotes int
	maxChars int

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

// NewSummarizer builds a weekly release summarizer. cache may be nil.
func NewSummarizer(chat model.Provider, cache *summarize.Cache, maxNotes, maxChars int) *Summarizer {
	if maxChars <= 0 {
		maxChars = DefaultSummaryMaxChars
	}
	return &Summarizer{chat: chat, cache: cache, maxNotes: maxNotes, maxChars: maxChars}
}

// SummarizeRepo makes at most one model call for the whole week and returns one
// Chinese summary. A cache hit costs nothing; a model failure degrades to the
// original notes instead of dropping the week.
func (s *Summarizer) SummarizeRepo(ctx context.Context, repo, weekKey string, cands []candidate) SummaryResult {
	if len(cands) == 0 {
		return SummaryResult{}
	}
	key := s.cacheKey(weekKey, cands)
	if s.cache != nil {
		if zh, ok := s.cache.Get(repo, key); ok {
			return SummaryResult{SummaryZh: zh, Source: "model"}
		}
	}
	if s.chat != nil {
		res, err := s.chat.Complete(ctx, SystemPrompt, buildUserPrompt(repo, cands, s.maxNotes, s.maxChars))
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
			if zh, ok := parseWeeklySummary(res.Text); ok {
				zh, truncated := s.cap(zh)
				if truncated {
					s.mu.Lock()
					s.trunc++
					s.mu.Unlock()
				}
				if s.cache != nil {
					s.cache.Put(repo, key, zh)
				}
				return SummaryResult{SummaryZh: zh, Source: "model", Truncated: truncated}
			}
		}
	}
	zh, truncated := s.cap(degradeSummary(cands))
	return SummaryResult{SummaryZh: zh, Source: "original", Truncated: truncated}
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
		Repo      string `json:"repo"`
		SummaryZh string `json:"summaryZh"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err == nil {
		zh := sanitize(resp.SummaryZh)
		return zh, zh != ""
	}
	// Not JSON: treat a short prose answer as the summary, anything else
	// (an error blob, a long code block) as a failure so we degrade instead.
	zh := sanitize(text)
	if zh == "" || utf8.RuneCountInString(zh) > 4000 {
		return "", false
	}
	return zh, true
}

var (
	reMDLink = regexp.MustCompile(`!?\[[^\]]*\]\([^)]*\)`)
	reHTML   = regexp.MustCompile(`<[^>]*>`)
	reURL    = regexp.MustCompile(`https?://\S+`)
)

func sanitize(s string) string {
	s = strings.TrimSpace(s)
	s = reMDLink.ReplaceAllString(s, "")
	s = reHTML.ReplaceAllString(s, "")
	s = reURL.ReplaceAllString(s, "")
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// degradeSummary is the no-model fallback: it keeps every version's own notes
// so the week is not silently lost, then trims to the configured budget.
func degradeSummary(cands []candidate) string {
	parts := make([]string, 0, len(cands))
	for _, c := range cands {
		text := sanitize(c.rel.Body)
		if text == "" {
			continue
		}
		parts = append(parts, c.tagName()+": "+text)
	}
	if len(parts) == 0 {
		return fmt.Sprintf("本周 %d 个版本（未获取到变更说明）", len(cands))
	}
	return "本周 " + fmt.Sprint(len(cands)) + " 个版本。" + strings.Join(parts, "；")
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
