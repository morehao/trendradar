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

// SummaryResult is one summarized release. Source is "model" or "original".
type SummaryResult struct {
	SummaryZh string
	Source    string
}

// candidate is one release/tag to summarize and emit.
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

// cacheKey includes the notes hash so an edited release body re-summarizes.
func (c candidate) cacheKey() string {
	h := sha256.Sum256([]byte(c.rel.Body))
	return c.tagName() + "|" + hex.EncodeToString(h[:6])
}

// Summarizer writes release summaries with a persistent cache and per-item
// degradation (a failure never aborts the run).
type Summarizer struct {
	chat      model.Provider
	cache     *summarize.Cache
	maxNotes  int
	batchSize int

	mu    sync.Mutex
	usage model.Usage
	calls int
}

// Stats returns accumulated token usage and real provider call count.
func (s *Summarizer) Stats() (model.Usage, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usage, s.calls
}

// NewSummarizer builds a release summarizer. cache may be nil.
func NewSummarizer(chat model.Provider, cache *summarize.Cache, maxNotes int) *Summarizer {
	return &Summarizer{chat: chat, cache: cache, maxNotes: maxNotes, batchSize: 5}
}

// Summarize returns a result for every candidate, keyed by repo+"\x00"+tag.
func (s *Summarizer) Summarize(ctx context.Context, cands []candidate) map[string]SummaryResult {
	out := make(map[string]SummaryResult, len(cands))
	var pending []candidate
	for _, c := range cands {
		k := c.repoFull + "\x00" + c.tagName()
		if s.cache != nil {
			if zh, ok := s.cache.Get(c.repoFull, c.cacheKey()); ok {
				out[k] = SummaryResult{SummaryZh: zh, Source: "model"}
				continue
			}
		}
		pending = append(pending, c)
	}
	for start := 0; start < len(pending); start += s.batchSize {
		end := start + s.batchSize
		if end > len(pending) {
			end = len(pending)
		}
		batch := pending[start:end]
		s.summarizeBatch(ctx, batch, out)
	}
	return out
}

func (s *Summarizer) summarizeBatch(ctx context.Context, batch []candidate, out map[string]SummaryResult) {
	got := map[string]string{}
	if s.chat != nil {
		if res, err := s.chat.Complete(ctx, SystemPrompt, buildUserPrompt(batch, s.maxNotes)); err == nil {
			got = parseReleaseSummaries(res.Text)
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
		}
	}
	for _, c := range batch {
		k := c.repoFull + "\x00" + c.tagName()
		if zh, ok := got[c.repoFull+"\x00"+c.tagName()]; ok && validReleaseSummary(zh) {
			out[k] = SummaryResult{SummaryZh: zh, Source: "model"}
			if s.cache != nil {
				s.cache.Put(c.repoFull, c.cacheKey(), zh)
			}
			continue
		}
		out[k] = SummaryResult{SummaryZh: originalSummary(c), Source: "original"}
	}
}

type releaseSummaryResponse struct {
	Summaries []struct {
		Repo      string `json:"repo"`
		Tag       string `json:"tag"`
		SummaryZh string `json:"summaryZh"`
	} `json:"summaries"`
}

func parseReleaseSummaries(raw string) map[string]string {
	raw = stripFence(strings.TrimSpace(raw))
	var resp releaseSummaryResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil
	}
	out := make(map[string]string, len(resp.Summaries))
	for _, s := range resp.Summaries {
		zh := sanitize(s.SummaryZh)
		if zh == "" {
			continue
		}
		out[s.Repo+"\x00"+s.Tag] = zh
	}
	return out
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

func validReleaseSummary(s string) bool {
	if s == "" || utf8.RuneCountInString(s) > 180 {
		return false
	}
	return true
}

// originalSummary degrades to the first sentence of the notes, or a clear
// placeholder when the notes are empty.
func originalSummary(c candidate) string {
	text := sanitize(c.rel.Body)
	if text == "" {
		return fmt.Sprintf("版本 %s（未获取到变更说明）", c.tagName())
	}
	if idx := strings.IndexAny(text, "。.!?！？\n"); idx > 0 {
		text = text[:idx+1]
	}
	if utf8.RuneCountInString(text) > 120 {
		text = string([]rune(text)[:120])
	}
	return text
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
