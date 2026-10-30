// Package parse turns trending HTML into digest.RepoSnapshot values.
package parse

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/morehao/trendradar/internal/digest"
)

var starsPeriodRe = regexp.MustCompile(`(?i)([0-9][0-9,\.]*)\s+stars?\s+(today|this week|this month)`)

// Stats counts rows and missing required fields for one page.
type Stats struct {
	Total           int
	MissingFullName int
	MissingURL      int
}

// ParseTrending parses a single trending page. pageLang is the language of the
// requested page ("all" for the global page); it is only used as a fallback when
// an article does not advertise its own language.
func ParseTrending(body []byte, pageLang string) ([]digest.RepoSnapshot, Stats, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, Stats{}, err
	}

	var out []digest.RepoSnapshot
	var st Stats

	doc.Find("article.Box-row").Each(func(i int, s *goquery.Selection) {
		st.Total++
		href, ok := repoHref(s)
		if !ok {
			st.MissingFullName++
			st.MissingURL++
			return
		}
		full := fullNameFromHref(href)
		if full == "" {
			st.MissingFullName++
			st.MissingURL++
			return
		}

		lang := strings.TrimSpace(s.Find(`span[itemprop="programmingLanguage"]`).First().Text())
		normLang := normalizeLanguage(lang)
		if normLang == "unknown" && pageLang != "" && pageLang != "all" {
			normLang = normalizeLanguage(pageLang)
		}

		out = append(out, digest.RepoSnapshot{
			FullName:      full,
			URL:           "https://github.com" + href,
			Description:   digest.StrPtr(strings.TrimSpace(s.Find("p.col-9").First().Text())),
			Language:      normLang,
			Stars:         parseCount(s.Find(`a[href$="/stargazers"]`).First().Text()),
			StarsInPeriod: parseStarsInPeriod(s.Text()),
			Forks:         digest.IntPtr(parseCount(s.Find(`a[href$="/forks"]`).First().Text())),
			Rank:          i + 1,
			Source:        digest.SourceTrendingHTML,
		})
	})

	return out, st, nil
}

// ParseAll parses every fetched page, de-duplicates across languages and builds
// a parse report.
func ParseAll(pages map[string][]byte) ([]digest.RepoSnapshot, digest.ParseReport, error) {
	langs := orderLanguages(pages)
	var all [][]digest.RepoSnapshot
	var stats []Stats
	for _, lang := range langs {
		repos, st, err := ParseTrending(pages[lang], lang)
		if err != nil {
			return nil, digest.ParseReport{}, err
		}
		all = append(all, repos)
		stats = append(stats, st)
	}
	return Merge(all), Detect(stats, 0.20), nil
}

// repoHref finds the repository link inside the heading, skipping the login
// redirect anchors GitHub renders for the star button.
func repoHref(s *goquery.Selection) (string, bool) {
	var href string
	var ok bool
	s.Find("h2.h3.lh-condensed a[href]").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		h, exists := a.Attr("href")
		if !exists {
			return true
		}
		if !strings.HasPrefix(h, "/") || strings.Contains(h, "login") || strings.Contains(h, "?return_to=") {
			return true
		}
		if len(strings.Split(strings.Trim(h, "/"), "/")) == 2 {
			href, ok = h, true
			return false
		}
		return true
	})
	return href, ok
}

func fullNameFromHref(href string) string {
	parts := strings.Split(strings.Trim(href, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

func parseStarsInPeriod(text string) int {
	m := starsPeriodRe.FindStringSubmatch(text)
	if len(m) < 2 {
		return 0
	}
	return parseCount(m[1])
}

func parseCount(s string) int {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0
	}
	mult := 1
	switch {
	case strings.HasSuffix(s, "k"):
		mult, s = 1000, strings.TrimSuffix(s, "k")
	case strings.HasSuffix(s, "m"):
		mult, s = 1_000_000, strings.TrimSuffix(s, "m")
	}
	s = strings.ReplaceAll(s, ",", "")
	s = strings.TrimSpace(s)
	if i, err := strconv.Atoi(s); err == nil {
		return i * mult
	}
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	i, _ := strconv.Atoi(b.String())
	return i * mult
}

func orderLanguages(pages map[string][]byte) []string {
	langs := make([]string, 0, len(pages))
	for l := range pages {
		langs = append(langs, l)
	}
	// sort with "all" first, then lexicographic for determinism.
	sortStrings(langs)
	for i, l := range langs {
		if l == "all" && i != 0 {
			langs[0], langs[i] = langs[i], langs[0]
			break
		}
	}
	return langs
}
