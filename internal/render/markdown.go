package render

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/i18n"
)

// Markdown renders a digest as a markdown document with YAML frontmatter and a
// table per language, in the default (zh) output language. It keeps the legacy
// summary-degraded banner behavior by deriving a default banner from d.Degraded,
// and uses the legacy per-repo-language layout (no per-page information
// available here).
func Markdown(d digest.Digest) []byte {
	return MarkdownLang(d, "")
}

// MarkdownLang is Markdown in an explicit output language (empty means zh).
func MarkdownLang(d digest.Digest, lang string) []byte {
	l := i18n.MustResolve(lang)
	var banners []string
	if d.Degraded {
		banners = append(banners, l.Render.DegradedBanner+"\n")
	}
	return MarkdownWithBannersLang(d, nil, banners, lang)
}

// defaultDegradedBanner is the default (zh) trending degraded banner, kept for
// callers that predate the language parameter.
var defaultDegradedBanner = i18n.MustResolve(i18n.DefaultLanguage).Render.DegradedBanner + "\n"

// MarkdownWithBanners renders the digest and injects each banner line as a
// blockquote right after the title. Banners let callers explain non-summary
// degradation (e.g. search-API fallback has no per-period deltas) so a degraded
// digest is never silently shown as if it were clean.
//
// byPage drives sectioning. When non-nil it maps a requested page language to
// the repos scraped from that page (page order preserved, not de-duplicated
// across pages); the document then shows exactly one section per entry in
// d.Languages, in that order, with empty sections kept for languages that
// yielded no repos. The "all" page's repos stay together under "## all" rather
// than being split by each repo's own language. When byPage is nil (search
// fallback, --input replay, or Markdown()) the document falls back to the
// legacy layout: one section per repo's own primary language.
func MarkdownWithBanners(d digest.Digest, byPage map[string][]digest.RepoDigest, banners []string) []byte {
	return MarkdownWithBannersLang(d, byPage, banners, "")
}

// MarkdownWithBannersLang is MarkdownWithBanners in an explicit output language
// (empty means zh). The fixed strings (title, table header) come from the
// language's RenderTexts; per-repo summaries are read from r.Summary.
func MarkdownWithBannersLang(d digest.Digest, byPage map[string][]digest.RepoDigest, banners []string, lang string) []byte {
	l := i18n.MustResolve(lang)
	rt := l.Render
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "date: %s\n", d.Date)
	fmt.Fprintf(&b, "period: %s\n", d.Period)
	fmt.Fprintf(&b, "languages: [%s]\n", strings.Join(d.Languages, ", "))
	fmt.Fprintf(&b, "count: %d\n", d.Count)
	fmt.Fprintf(&b, "degraded: %t\n", d.Degraded)
	fmt.Fprintf(&b, "contentHash: %s\n", d.ContentHash)
	fmt.Fprintf(&b, "coreVersion: %s\n", digest.Str(d.CoreVersion))
	gen := ""
	if d.GeneratedAt != nil {
		gen = d.GeneratedAt.UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(&b, "generatedAt: %s\n", gen)
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s %s\n\n", d.Date, rt.DigestTitle)
	for _, banner := range banners {
		if banner == "" {
			continue
		}
		b.WriteString(banner)
		b.WriteString("\n")
	}

	for _, section := range buildSections(d, byPage) {
		fmt.Fprintf(&b, "## %s\n\n", section.lang)
		b.WriteString(rt.TableHeader + "\n")
		b.WriteString(rt.TableSeparator + "\n")
		for _, r := range section.repos {
			fmt.Fprintf(&b, "| [%s](%s) | %s | +%s | %s |\n",
				cell(r.FullName), r.URL, thousands(r.Stars), thousands(r.StarsInPeriod), cell(r.Summary))
		}
		b.WriteString("\n")
	}
	return []byte(b.String())
}

// section is one rendered "## <lang>" block.
type section struct {
	lang  string
	repos []digest.RepoDigest
}

// buildSections decides the document's sections.
//
// With per-page data (byPage non-nil and a non-empty d.Languages), it emits one
// section per requested language in d.Languages order — including empty ones —
// sourcing repos from byPage[lang]. A repo that appears on multiple pages shows
// up under each of those sections (cross-page duplication is intentional).
//
// Without per-page data it falls back to grouping by each repo's own primary
// language, ordered with requested languages first then extras sorted (the
// legacy behavior).
func buildSections(d digest.Digest, byPage map[string][]digest.RepoDigest) []section {
	if byPage == nil || len(d.Languages) == 0 {
		groups := groupByLanguage(d.Repos)
		out := make([]section, 0, len(groups))
		for _, lang := range orderedRepoLanguages(d, groups) {
			out = append(out, section{lang: lang, repos: groups[lang]})
		}
		return out
	}
	out := make([]section, 0, len(d.Languages))
	for _, lang := range d.Languages {
		out = append(out, section{lang: lang, repos: sortByRank(byPage[lang])})
	}
	return out
}

// sortByRank returns a copy of repos ordered by rank ascending (then fullName
// for determinism), so a section reads top-down by the page's own order.
func sortByRank(repos []digest.RepoDigest) []digest.RepoDigest {
	out := append([]digest.RepoDigest(nil), repos...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rank != out[j].Rank {
			return out[i].Rank < out[j].Rank
		}
		return out[i].FullName < out[j].FullName
	})
	return out
}

// cell keeps a value on one table row: newlines collapse and pipes are escaped.
func cell(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.TrimSpace(s)
}

func groupByLanguage(repos []digest.RepoDigest) map[string][]digest.RepoDigest {
	m := make(map[string][]digest.RepoDigest)
	for _, r := range repos {
		m[r.Language] = append(m[r.Language], r)
	}
	return m
}

func orderedRepoLanguages(d digest.Digest, groups map[string][]digest.RepoDigest) []string {
	seen := make(map[string]bool, len(groups))
	var out []string
	for _, l := range d.Languages {
		if _, ok := groups[l]; ok && !seen[l] {
			out = append(out, l)
			seen[l] = true
		}
	}
	var extra []string
	for l := range groups {
		if !seen[l] {
			extra = append(extra, l)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

func thousands(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	res := strings.Join(parts, ",")
	if neg {
		return "-" + res
	}
	return res
}
