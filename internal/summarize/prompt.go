package summarize

import (
	"strconv"
	"strings"

	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/i18n"
)

// SystemPrompt is the default (Chinese) trending summarization system prompt. It
// is retained for tests and callers that assume the default language; the
// pipeline uses lang.TrendingSystem so the prompt follows
// config.language.outputLanguage. A test asserts it equals the zh registry entry.
var SystemPrompt = i18n.MustResolve(i18n.DefaultLanguage).TrendingSystem

// systemPromptFor returns the trending system prompt for lang.
func systemPromptFor(lang i18n.OutputLanguage) string { return lang.TrendingSystem }

// userPromptFor renders the batch prompt in lang. It encodes the batch in the
// same machine-parseable form regardless of language so the offline Fake provider
// (which keys off the "REPO=\tDESC=" lines) keeps working in every language.
func userPromptFor(lang i18n.OutputLanguage, batch []digest.RepoSnapshot, byRepo map[string]Context) string {
	var b strings.Builder
	b.WriteString(lang.TrendingUserIntro)
	for _, r := range batch {
		desc := strings.TrimSpace(digest.Str(r.Description))
		if desc == "" {
			desc = noneDescription(lang)
		}
		desc = strings.ReplaceAll(desc, "\n", " ")
		b.WriteString("REPO=" + r.FullName + "\tDESC=" + desc + "\n")

		meta := "META=lang=" + nonEmpty(r.Language, "unknown") +
			"; stars=" + strconv.Itoa(r.Stars) +
			"; starsPeriod=" + strconv.Itoa(r.StarsInPeriod) +
			"; rank=" + strconv.Itoa(r.Rank)
		b.WriteString(meta + "\n")
		if c, ok := byRepo[r.FullName]; ok {
			if len(c.Topics) > 0 {
				b.WriteString("TOPICS=" + strings.Join(c.Topics, ",") + "\n")
			}
			if excerpt := oneLine(c.ReadmeExcerpt); excerpt != "" {
				b.WriteString("README_EXCERPT=" + excerpt + "\n")
			}
		}
	}
	return b.String()
}

// noneDescription is the "(no description)" placeholder in lang. The zh form is
// kept byte-identical to the pre-i18n prompt.
func noneDescription(lang i18n.OutputLanguage) string {
	if lang.ID == "en" {
		return "(no description)"
	}
	return "(无描述)"
}

// buildUserPrompt is the default-language (Chinese) convenience wrapper kept for
// tests and callers that predate the language parameter.
func buildUserPrompt(batch []digest.RepoSnapshot, byRepo map[string]Context) string {
	return userPromptFor(i18n.MustResolve(i18n.DefaultLanguage), batch, byRepo)
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}
