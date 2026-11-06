package summarize

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/i18n"
)

// ErrBadOutput marks model output that could not be decoded into valid summaries.
var ErrBadOutput = errors.New("summarize: invalid model output")

const maxSummaryRunes = 80 // hard cap; product target is 60

var (
	reMarkdownLink = regexp.MustCompile(`!?\[[^\]]*\]\([^)]*\)`)
	reHTMLTag      = regexp.MustCompile(`<[^>]*>`)
	reBareURL      = regexp.MustCompile(`https?://\S+`)
)

type response struct {
	Summaries []struct {
		FullName string `json:"fullName"`
		Summary  string `json:"summary"`
	} `json:"summaries"`
}

// parseResponse decodes the model output and keeps only valid summaries whose
// fullName came from the input batch. It is kept for callers that only need the
// valid map; the repair path uses parseAndValidate.
func parseResponse(raw string, batch []digest.RepoSnapshot) (map[string]string, error) {
	out, _, err := parseAndValidate(raw, batch)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no valid summaries for input batch", ErrBadOutput)
	}
	return out, nil
}

// parseAndValidate decodes model output, sanitises every summary, and returns
// the accepted map plus the list of batch repos still missing/invalid (which
// drives the repair pass). It validates against the default (Chinese) language;
// parseAndValidateLang is the language-aware form the pipeline uses.
func parseAndValidate(raw string, batch []digest.RepoSnapshot) (map[string]string, []string, error) {
	return parseAndValidateLang(raw, batch, i18n.MustResolve(i18n.DefaultLanguage))
}

// parseAndValidateLang is parseAndValidate with an explicit output language, so
// the "is this actually written in <lang>" gate matches what the prompt asked
// for (Han characters for zh, letters for en).
func parseAndValidateLang(raw string, batch []digest.RepoSnapshot, lang i18n.OutputLanguage) (map[string]string, []string, error) {
	raw = stripFence(strings.TrimSpace(raw))
	var resp response
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrBadOutput, err)
	}
	descByName := make(map[string]string, len(batch))
	allowed := make(map[string]bool, len(batch))
	for _, r := range batch {
		allowed[r.FullName] = true
		descByName[r.FullName] = digest.Str(r.Description)
	}
	out := make(map[string]string, len(resp.Summaries))
	for _, s := range resp.Summaries {
		sum := sanitizeSummary(s.Summary)
		if allowed[s.FullName] && validSummaryLang(sum, descByName[s.FullName], lang) {
			out[s.FullName] = sum
		}
	}
	invalid := make([]string, 0, len(batch))
	for _, r := range batch {
		if _, ok := out[r.FullName]; !ok {
			invalid = append(invalid, r.FullName)
		}
	}
	return out, invalid, nil
}

// sanitizeSummary removes markup that must never reach the rendered digest:
// Markdown links/images, HTML tags, bare URLs, and control characters.
func sanitizeSummary(s string) string {
	s = strings.TrimSpace(s)
	s = reMarkdownLink.ReplaceAllString(s, "")
	s = reHTMLTag.ReplaceAllString(s, "")
	s = reBareURL.ReplaceAllString(s, "")
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// validSummary is the default-language (Chinese) validity gate, kept for tests.
func validSummary(s, desc string) bool {
	return validSummaryLang(s, desc, i18n.MustResolve(i18n.DefaultLanguage))
}

// validSummaryLang is the language-aware validity gate. For zh it requires at
// least one Han character (see the long comment below); for en it requires at
// least one Latin letter. Both reject empty output, over-long output, and a
// verbatim copy of the description.
func validSummaryLang(s, desc string, lang i18n.OutputLanguage) bool {
	if s == "" {
		return false
	}
	total := utf8.RuneCountInString(s)
	if total > maxSummaryRunes {
		return false
	}
	if strings.TrimSpace(s) == strings.TrimSpace(desc) {
		return false
	}
	if lang.ID == "en" {
		return hasLatinLetter(s)
	}
	// 只要求「是中文」——即至少含一个汉字——而不是要求汉字占比达到某个比例。
	// 早先用「汉字占比 ≥ 40%」当门槛，本意是拦截「模型把英文原文照抄回来，
	// 没有真正写中文摘要」。但这个比例门槛会误杀一类完全合法的摘要：仓库名/描述
	// 本身几乎全是英文专有名词（如 AI Studio Playground / OpenAI / Gemini / API），
	// 即便模型写出通顺的简体中文摘要，汉字占比也远低于 40%，于是被判定为无效、
	// 重试也救不回，最终降级为 [原文]。英文占比高本身不是问题——只要摘要确实是
	// 中文（含汉字）且不是照抄原文描述，就应接受。
	return hasHan(s)
}

// hasLatinLetter reports whether s contains at least one Unicode Latin letter.
// It is the en counterpart of hasHan: pure-CJK or pure-symbol output from the
// model is rejected when English was requested.
func hasLatinLetter(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Latin, r) {
			return true
		}
	}
	return false
}

// hasHan reports whether s contains at least one Han character. It is the
// "is this actually written in Chinese" gate: pure-English (or pure-symbol)
// output from the model is rejected, while a genuinely Chinese summary of a
// Latin-heavy repository is accepted regardless of the Han ratio.
func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// stripFence removes a surrounding ```json ... ``` code fence if present.
func stripFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimPrefix(s, "json")
	s = strings.TrimPrefix(s, "\n")
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}
