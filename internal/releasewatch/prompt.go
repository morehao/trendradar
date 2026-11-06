package releasewatch

import (
	"fmt"
	"strings"

	"github.com/morehao/trendradar/internal/i18n"
)

// PromptVersion is bumped when the release-summary prompt or output contract
// changes. release-v2 summarizes one repo's whole week in a single call; v1
// summarized each tag separately, so old cache entries must not be reused.
// release-v3 asks for a lightly-structured summary (bold labels + bullets inside
// the JSON string) so the rendered document reads in sections instead of one
// dense paragraph. release-v4 renames the output key summaryZh→summary and makes
// the prompt language-driven; the cache namespace also carries the language, but
// the version bump additionally invalidates the whole prior series.
const PromptVersion = "release-v4"

// DefaultSummaryMaxChars caps the weekly summary when config leaves it unset.
const DefaultSummaryMaxChars = 1200

// SystemPrompt is the default (Chinese) release-summary system prompt, kept for
// tests and callers that assume the default language; the pipeline uses
// lang.ReleaseSystem so the prompt follows config.language.outputLanguage. A
// test asserts it equals the zh registry entry.
var SystemPrompt = i18n.MustResolve(i18n.DefaultLanguage).ReleaseSystem

// buildUserPrompt encodes one repo's week as delimited data in lang. The
// <RELEASE_NOTES> fence keeps untrusted changelog text visually separate from
// instructions. maxNotes is the *total* notes budget for the repo so a repo with
// many tags cannot multiply the prompt size.
func buildUserPrompt(lang i18n.OutputLanguage, repo string, batch []candidate, maxNotes, maxChars int) string {
	if maxNotes <= 0 {
		maxNotes = 4000
	}
	if maxChars <= 0 {
		maxChars = DefaultSummaryMaxChars
	}
	per := maxNotes
	if n := len(batch); n > 1 {
		per = maxNotes / n
		if per < 200 {
			per = 200
		}
	}
	var b strings.Builder
	intro := fmt.Sprintf(lang.ReleaseWeekIntro, len(batch), maxChars)
	fmt.Fprintf(&b, "%s %s\n\n", repoLabel(lang, repo), intro)
	for _, c := range batch {
		notes := truncateRunes(strings.TrimSpace(c.rel.Body), per)
		notes = strings.ReplaceAll(notes, "\r\n", "\n")
		fmt.Fprintf(&b, "TAG=%s\n<RELEASE_NOTES>\n%s\n</RELEASE_NOTES>\n\n", c.tagName(), notes)
	}
	return b.String()
}

// buildItemPrompt encodes ONE version as delimited data in lang, asking for a
// summary of just that release. The <RELEASE_NOTES> fence keeps untrusted
// changelog text visually separate from instructions. maxNotes caps the notes so
// a single noisy changelog cannot flood the prompt.
func buildItemPrompt(lang i18n.OutputLanguage, repo string, c candidate, maxNotes, maxChars int) string {
	if maxNotes <= 0 {
		maxNotes = 4000
	}
	if maxChars <= 0 {
		maxChars = DefaultSummaryMaxChars
	}
	notes := truncateRunes(strings.TrimSpace(c.rel.Body), maxNotes)
	notes = strings.ReplaceAll(notes, "\r\n", "\n")
	var b strings.Builder
	if lang.ID == "en" {
		fmt.Fprintf(&b, "Repo: %s\nVersion: %s\n%s\n\n", repo, c.tagName(), fmt.Sprintf(lang.ReleaseItemIntro, maxChars))
	} else {
		fmt.Fprintf(&b, "仓库：%s\n版本：%s\n%s\n\n", repo, c.tagName(), fmt.Sprintf(lang.ReleaseItemIntro, maxChars))
	}
	fmt.Fprintf(&b, "TAG=%s\n<RELEASE_NOTES>\n%s\n</RELEASE_NOTES>\n", c.tagName(), notes)
	return b.String()
}

// repoLabel renders the "repo: <name>" prefix in lang. The zh form matches the
// pre-i18n prompt byte-for-byte.
func repoLabel(lang i18n.OutputLanguage, repo string) string {
	if lang.ID == "en" {
		return "Repo: " + repo
	}
	return "仓库：" + repo
}

// noChangelogOne is the per-version degrade fallback in lang.
func noChangelogOne(lang i18n.OutputLanguage, tag string) string {
	if lang.ID == "en" {
		return "**" + tag + "**\nNo changelog provided"
	}
	return "**" + tag + "**\n未提供变更说明"
}

func truncateRunes(s string, n int) string {
	if n <= 0 {
		n = 4000
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
