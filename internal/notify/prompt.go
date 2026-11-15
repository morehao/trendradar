package notify

// prompt.go builds the model prompts and orchestrates the model call with a
// rule-based fallback. Each section is one completion: the trending leaderboard
// in one call, the subscribed-repo releases in another (design §5.1/§5.2).
//
// Degrade policy (design §5.2): if the provider is nil, the call errors, the
// provider signals a fallback (Degraded), or the model returns empty text, we
// fall back to the rule-based summary so a push still goes out. summarySource is
// recorded as "model" or "fallback" so the caller can surface degradation.

import (
	"context"
	"fmt"
	"strings"

	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/i18n"
	"github.com/morehao/trendradar/internal/model"
	"github.com/morehao/trendradar/internal/releasedigest"
)

// builtBody is the outcome of summarizing one section.
type builtBody struct {
	text   string
	source string // SourceModel | SourceFallback
	usage  model.Usage
	calls  int
}

// trendingUserPrompt assembles the user message for the trending section: the
// compact leaderboard list plus a short instruction header.
func trendingUserPrompt(d digest.Digest, maxRepos int) string {
	items, _, rest := compactTrendingList(d, maxRepos)
	var b strings.Builder
	b.WriteString("Leaderboard data (one repository per line):\n")
	for _, it := range items {
		b.WriteString("- ")
		b.WriteString(it)
		b.WriteString("\n")
	}
	if rest > 0 {
		fmt.Fprintf(&b, "(%d more repositories are not listed.)\n", rest)
	}
	return b.String()
}

// releasesUserPrompt assembles the user message for the releases section.
func releasesUserPrompt(rel releasedigest.ReleaseDigest, maxRepos int) string {
	items, _, rest := compactReleasesList(rel, maxRepos)
	var b strings.Builder
	b.WriteString("Repositories with new versions this period (one per line):\n")
	for _, it := range items {
		b.WriteString("- ")
		b.WriteString(it)
		b.WriteString("\n")
	}
	if rest > 0 {
		fmt.Fprintf(&b, "(%d more repositories are not listed.)\n", rest)
	}
	return b.String()
}

// buildTrending produces the trending body: try the model, degrade to rule-based
// on any failure. Never returns an error that would fail the run.
func buildTrending(ctx context.Context, opt Options, d digest.Digest, lang i18n.OutputLanguage, limit int) (builtBody, error) {
	subject := trendingSubject(lang, d.Date, d.Period)
	maxRepos := effectiveMaxReposPerSection(opt.Cfg)
	fallback := func() builtBody {
		return builtBody{text: ruleBasedTrending(lang, d, maxRepos), source: SourceFallback}
	}

	if opt.Provider == nil {
		return fallback(), nil
	}

	system := fmt.Sprintf(lang.NotifyTrendingSystem, limit)
	user := trendingUserPrompt(d, maxRepos)
	res, err := opt.Provider.Complete(ctx, system, user)
	if err != nil || res.Degraded {
		return fallback(), nil
	}
	summary := strings.TrimSpace(res.Text)
	if summary == "" {
		return fallback(), nil
	}
	return builtBody{
		text:   assembleWithSummary(subject, summary),
		source: SourceModel,
		usage:  res.Usage,
		calls:  1,
	}, nil
}

// buildReleases produces the releases body: try the model, degrade to rule-based
// on any failure. Never returns an error that would fail the run.
func buildReleases(ctx context.Context, opt Options, rel releasedigest.ReleaseDigest, lang i18n.OutputLanguage, limit int) (builtBody, error) {
	subject := releasesSubject(lang, rel)
	maxRepos := effectiveMaxReposPerSection(opt.Cfg)
	fallback := func() builtBody {
		return builtBody{text: ruleBasedReleases(lang, rel, maxRepos), source: SourceFallback}
	}

	if opt.Provider == nil {
		return fallback(), nil
	}

	system := fmt.Sprintf(lang.NotifyReleasesSystem, limit)
	user := releasesUserPrompt(rel, maxRepos)
	res, err := opt.Provider.Complete(ctx, system, user)
	if err != nil || res.Degraded {
		return fallback(), nil
	}
	summary := strings.TrimSpace(res.Text)
	if summary == "" {
		return fallback(), nil
	}
	return builtBody{
		text:   assembleWithSummary(subject, summary),
		source: SourceModel,
		usage:  res.Usage,
		calls:  1,
	}, nil
}
