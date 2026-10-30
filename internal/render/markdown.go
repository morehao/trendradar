package render

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/morehao/trendradar/internal/digest"
)

// Markdown renders a digest as a markdown document with YAML frontmatter and a
// table per language.
func Markdown(d digest.Digest) []byte {
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
	fmt.Fprintf(&b, "# %s 趋势摘要\n\n", d.Date)
	if d.Degraded {
		b.WriteString("> ⚠️ 本次摘要有降级：模型不可用或返回了无效结果，部分条目为英文原文（summarySource=original）。\n\n")
	}

	groups := groupByLanguage(d.Repos)
	for _, lang := range orderedRepoLanguages(d, groups) {
		fmt.Fprintf(&b, "## %s\n\n", lang)
		b.WriteString("| 仓库 | Stars | 今日新增 | 摘要 |\n")
		b.WriteString("| --- | ---: | ---: | --- |\n")
		for _, r := range groups[lang] {
			fmt.Fprintf(&b, "| [%s](%s) | %s | +%s | %s |\n",
				cell(r.FullName), r.URL, thousands(r.Stars), thousands(r.StarsInPeriod), cell(r.SummaryZh))
		}
		b.WriteString("\n")
	}
	return []byte(b.String())
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
