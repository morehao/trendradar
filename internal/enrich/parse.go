package enrich

import (
	"regexp"
	"sort"
	"strings"
)

var (
	reTopic         = regexp.MustCompile(`href="/topics/([^"/?]+)"`)
	reDefaultBranch = regexp.MustCompile(`"defaultBranch":"([^"]+)"`)

	reHTMLTag    = regexp.MustCompile(`<[^>]+>`)
	reMDImage    = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	reMDLink     = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	reLeadMark   = regexp.MustCompile(`^[#>*+\-\s]+`)
	reWhitespace = regexp.MustCompile(`\s+`)
)

const maxTopics = 12

// readmeNames are tried in order; the common .md variants come first so a normal
// repo hits on the first request and the fan-out stays cheap.
var readmeNames = []string{"README.md", "readme.md", "README.rst", "README"}

// parseTopics extracts GitHub topic slugs from repository HTML. Topics render as
// <a href="/topics/<slug>">, which is also how the manual Track B evaluation
// harvested them without spending REST quota. Result is sorted for stable cache
// keys and capped to avoid prompt bloat.
func parseTopics(body []byte) []string {
	seen := make(map[string]bool)
	var out []string
	for _, m := range reTopic.FindAllSubmatch(body, -1) {
		t := string(m[1])
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	sort.Strings(out)
	if len(out) > maxTopics {
		out = out[:maxTopics]
	}
	return out
}

// parseDefaultBranch reads the default branch name embedded in the HTML. It is
// only a hint for the raw README URL; callers still fall back to HEAD/main.
func parseDefaultBranch(body []byte) string {
	if m := reDefaultBranch.FindSubmatch(body); m != nil {
		return string(m[1])
	}
	return ""
}

// cleanReadme turns raw README markdown into a compact single-line excerpt: it
// strips HTML, images and link targets, drops badge/code-fence lines, removes
// leading markdown markers and collapses whitespace. This is defensive against
// untrusted README content before it reaches the prompt.
func cleanReadme(text string) string {
	text = reHTMLTag.ReplaceAllString(text, " ")
	text = reMDImage.ReplaceAllString(text, " ")
	text = reMDLink.ReplaceAllString(text, "$1")
	var lines []string
	for _, ln := range strings.Split(text, "\n") {
		s := strings.TrimSpace(ln)
		if s == "" || strings.Contains(s, "shields.io") || strings.HasPrefix(s, "```") {
			continue
		}
		s = reLeadMark.ReplaceAllString(s, "")
		if s != "" {
			lines = append(lines, s)
		}
	}
	return strings.TrimSpace(reWhitespace.ReplaceAllString(strings.Join(lines, " "), " "))
}

// truncateRunes caps s to n runes so a multi-byte character is never split.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
