package notify

// render.go turns the run's structured data into (a) a compact, model-facing
// item list and (b) a rule-based fallback summary. It also holds the per-channel
// hard truncation (design §6.2) and the message subject lines.
//
// No document markdown is reused here: a chat message wants short, scannable,
// per-item text, not the data-repo tables. The rule-based fallback is what makes
// "a push always goes out" true even when the model is unavailable.

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/i18n"
	"github.com/morehao/trendradar/internal/releasedigest"
)

// per-channel byte ceilings from design §6.2 (UTF-8 bytes). summaryMaxChars=800
// is far below all of them, so this is a last-resort hard cap only.
const (
	limitWecom    = 4000
	limitDingtalk = 18000
	limitLark     = 28000
)

// truncationNote is appended when a body had to be hard-truncated to fit a
// channel's byte ceiling (design §6.2: "完整内容见数据仓 PR").
const (
	truncationNoteZh = "\n\n…（内容过长已截断，完整内容见数据仓库的 PR/提交）"
	truncationNoteEn = "\n\n…（truncated to fit; see the data repository PR/commit for the full content）"
)

// channelLimit returns the per-channel byte cap. name is "dingtalk"|"lark"|"wecom".
func channelLimit(name string) int {
	switch name {
	case "dingtalk":
		return limitDingtalk
	case "lark":
		return limitLark
	case "wecom":
		return limitWecom
	default:
		return limitWecom // most conservative by default
	}
}

// truncateForChannel caps body to the channel's byte ceiling (design §6.2). When
// maxBodyChars > 0 it is used as a uniform hard cap that overrides the
// per-channel default (design: ">0 为统一硬闸"). It truncates on a rune
// boundary, appends a truncation note, and never splits a UTF-8 sequence.
func truncateForChannel(body, channelName string, maxBodyChars int, lang i18n.OutputLanguage) string {
	limit := channelLimit(channelName)
	if maxBodyChars > 0 && maxBodyChars < limit {
		limit = maxBodyChars
	}
	if len(body) <= limit {
		return body
	}
	note := truncationNoteZh
	if lang.ID == "en" {
		note = truncationNoteEn
	}
	// Reserve room for the note, then trim to a rune boundary.
	budget := limit - len(note)
	if budget < 0 {
		budget = 0
	}
	cut := 0
	for i := range body {
		if i > budget {
			break
		}
		cut = i
	}
	// Advance cut to cover the rune that starts at cut (so i<=budget keeps the
	// full rune), then trim back to a valid boundary defensively.
	trimmed := body[:cut]
	for len(trimmed) > 0 && !utf8.ValidString(trimmed) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed + note
}

// runeLen returns the number of runes (used to report bodyChars; bytes are for
// truncation, chars are for the summary budget and the contract field).
func runeLen(s string) int { return utf8.RuneCountInString(s) }

// compactTrendingList renders the leaderboard as a compact item list fed to the
// model (or used verbatim by the rule-based fallback). It names at most
// maxRepos repos (already sorted by the digest) and reports the remainder.
//
// allowLinks controls whether each repo name is rendered as a markdown link
// [FullName](URL). Links are injected at assembly time only (never sent to the
// model); the plain-text tier passes false so a `text` channel never receives
// `[..](..)` syntax.
//
// Fields used (design §5.2): FullName, StarsInPeriod, Rank, Summary, Description.
func compactTrendingList(d digest.Digest, maxRepos int, allowLinks bool) (items []string, shown int, rest int) {
	repos := d.Repos
	if maxRepos > 0 && len(repos) > maxRepos {
		repos = repos[:maxRepos]
	}
	for _, r := range repos {
		items = append(items, formatTrendingItem(r, allowLinks))
	}
	shown = len(repos)
	rest = len(d.Repos) - shown
	if rest < 0 {
		rest = 0
	}
	return items, shown, rest
}

// formatTrendingItem renders one repo as "owner/repo — +N stars — summary/desc".
// When allowLinks is true and the repo carries a URL, the name becomes
// [owner/repo](url) so a link-capable channel renders it clickable; otherwise it
// stays plain FullName (URL empty → plain, no error).
func formatTrendingItem(r digest.RepoDigest, allowLinks bool) string {
	var b strings.Builder
	if allowLinks && r.URL != "" {
		b.WriteString("[")
		b.WriteString(r.FullName)
		b.WriteString("](")
		b.WriteString(r.URL)
		b.WriteString(")")
	} else {
		b.WriteString(r.FullName)
	}
	if r.Rank > 0 {
		fmt.Fprintf(&b, " (rank %d)", r.Rank)
	}
	if r.StarsInPeriod != 0 {
		fmt.Fprintf(&b, " — +%d stars", r.StarsInPeriod)
	}
	desc := strings.TrimSpace(r.Summary)
	if desc == "" {
		desc = strings.TrimSpace(digest.Str(r.Description))
	}
	if desc != "" {
		b.WriteString(" — ")
		b.WriteString(desc)
	}
	return b.String()
}

// ruleBasedTrending builds the fallback trending body from the compact list: one
// bullet per shown repo and a "remaining N" note. It never includes the subject
// line — the title lives solely on Message.Subject (single source of truth);
// including it here would duplicate it on channels that also render Subject.
func ruleBasedTrending(lang i18n.OutputLanguage, d digest.Digest, maxRepos int) string {
	return trendingListBody(lang, d, maxRepos, false)
}

// ruleBasedTrendingLinks is the link tier twin of ruleBasedTrending: same list
// but each repo name rendered as [FullName](URL). Used to build Message.BodyLink.
func ruleBasedTrendingLinks(lang i18n.OutputLanguage, d digest.Digest, maxRepos int) string {
	return trendingListBody(lang, d, maxRepos, true)
}

func trendingListBody(lang i18n.OutputLanguage, d digest.Digest, maxRepos int, allowLinks bool) string {
	items, _, rest := compactTrendingList(d, maxRepos, allowLinks)
	var b strings.Builder
	for _, it := range items {
		b.WriteString("- ")
		b.WriteString(it)
		b.WriteString("\n")
	}
	if rest > 0 {
		if lang.ID == "en" {
			fmt.Fprintf(&b, "\n(%d more not shown)", rest)
		} else {
			fmt.Fprintf(&b, "\n（其余 %d 个未列出）", rest)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// compactReleasesList renders subscribed repos that changed this period as a
// compact list fed to the model / used by the fallback. Only NewCount>0 are
// listed (design §5.2). Fields used: FullName, NewCount, LatestTag, Summary.
func compactReleasesList(rel releasedigest.ReleaseDigest, maxRepos int, allowLinks bool) (items []string, shown int, rest int) {
	changed := make([]releasedigest.RepoReleases, 0, len(rel.Repos))
	for _, r := range rel.Repos {
		if r.NewCount > 0 {
			changed = append(changed, r)
		}
	}
	total := len(changed)
	if maxRepos > 0 && len(changed) > maxRepos {
		changed = changed[:maxRepos]
	}
	for _, r := range changed {
		items = append(items, formatReleaseItem(r, allowLinks))
	}
	shown = len(changed)
	rest = total - shown
	if rest < 0 {
		rest = 0
	}
	return items, shown, rest
}

// formatReleaseItem renders one repo as "owner/repo — N new versions (latest X)
// — summary". When allowLinks is true and the repo carries a URL, the name
// becomes [owner/repo](url) (URL empty → plain FullName, no error).
func formatReleaseItem(r releasedigest.RepoReleases, allowLinks bool) string {
	var b strings.Builder
	if allowLinks && r.URL != "" {
		b.WriteString("[")
		b.WriteString(r.FullName)
		b.WriteString("](")
		b.WriteString(r.URL)
		b.WriteString(")")
	} else {
		b.WriteString(r.FullName)
	}
	fmt.Fprintf(&b, " — %d new version(s)", r.NewCount)
	if r.LatestTag != "" {
		fmt.Fprintf(&b, " (latest %s)", r.LatestTag)
	}
	if s := strings.TrimSpace(digest.Str(r.Summary)); s != "" {
		b.WriteString(" — ")
		b.WriteString(s)
	}
	return b.String()
}

// ruleBasedReleases builds the fallback releases body from the compact list. Like
// ruleBasedTrending it never includes the subject line — the title is carried
// only by Message.Subject.
func ruleBasedReleases(lang i18n.OutputLanguage, rel releasedigest.ReleaseDigest, maxRepos int) string {
	return releasesListBody(lang, rel, maxRepos, false)
}

// ruleBasedReleasesLinks is the link tier twin of ruleBasedReleases.
func ruleBasedReleasesLinks(lang i18n.OutputLanguage, rel releasedigest.ReleaseDigest, maxRepos int) string {
	return releasesListBody(lang, rel, maxRepos, true)
}

func releasesListBody(lang i18n.OutputLanguage, rel releasedigest.ReleaseDigest, maxRepos int, allowLinks bool) string {
	items, _, rest := compactReleasesList(rel, maxRepos, allowLinks)
	var b strings.Builder
	for _, it := range items {
		b.WriteString("- ")
		b.WriteString(it)
		b.WriteString("\n")
	}
	if rest > 0 {
		if lang.ID == "en" {
			fmt.Fprintf(&b, "\n(%d more repos not shown)", rest)
		} else {
			fmt.Fprintf(&b, "\n（其余 %d 个仓库未列出）", rest)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// trendingSubject is the title/first line of the trending message, e.g.
// "🏆 GitHub 趋势榜 · 2026-11-15（日榜）".
func trendingSubject(lang i18n.OutputLanguage, date, period string) string {
	periodLabel := lang.Render.Periods[period]
	if lang.ID == "en" {
		head := "🏆 GitHub Trending · " + date
		if periodLabel != "" {
			head += " (" + periodLabel + ")"
		}
		return head
	}
	head := "🏆 GitHub 趋势榜 · " + date
	if periodLabel != "" {
		head += "（" + periodLabel + "）"
	}
	return head
}

// releasesSubject is the title/first line of the releases message, e.g.
// "📦 订阅仓库本周变动（2026-11-15）".
func releasesSubject(lang i18n.OutputLanguage, rel releasedigest.ReleaseDigest) string {
	date := rel.Date
	if lang.ID == "en" {
		return "📦 Subscribed repos updated (" + date + ")"
	}
	return "📦 订阅仓库本周变动（" + date + "）"
}

// summaryBody returns the model summary trimmed as the final body. The subject is
// no longer prepended: the title lives solely on Message.Subject, so Body is pure
// content (no title duplication on channels that also render Subject).
func summaryBody(summary string) string {
	return strings.TrimSpace(summary)
}
