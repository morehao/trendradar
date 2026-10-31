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
		FullName  string `json:"fullName"`
		SummaryZh string `json:"summaryZh"`
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
// drives the repair pass).
func parseAndValidate(raw string, batch []digest.RepoSnapshot) (map[string]string, []string, error) {
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
		sum := sanitizeSummary(s.SummaryZh)
		if allowed[s.FullName] && validSummary(sum, descByName[s.FullName]) {
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

func validSummary(s, desc string) bool {
	if s == "" {
		return false
	}
	total := utf8.RuneCountInString(s)
	if total > maxSummaryRunes {
		return false
	}
	if total == 0 || float64(countHan(s))/float64(total) < 0.4 {
		return false
	}
	return strings.TrimSpace(s) != strings.TrimSpace(desc)
}

func countHan(s string) int {
	n := 0
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			n++
		}
	}
	return n
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
