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

// builtBody is the outcome of summarizing one section. text is the plain-text
// tier body (never contains the subject); bodyLink is the link-capable tier body
// (may be empty when no link-capable channel is targeted). Both are derived from
// the same single model/fallback summary so the model is called exactly once.
type builtBody struct {
	text     string
	bodyLink string
	source   string // SourceModel | SourceFallback
	usage    model.Usage
	calls    int
}

// trendingUserPrompt assembles the user message for the trending section: the
// compact leaderboard list plus a short instruction header.
func trendingUserPrompt(d digest.Digest, maxRepos int) string {
	// Model-facing material is always the plain tier (no links): links are injected
	// deterministically at assembly time, never handed to the model.
	items, _, rest := compactTrendingList(d, maxRepos, false)
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
	items, _, rest := compactReleasesList(rel, maxRepos, false)
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
//
// Two tiers share one model call: the plain tier's body is the model summary (or
// the rule-based plain list on fallback); the link tier's body is the same summary
// with a deterministic per-repo link list appended, built from the rule-based item
// list so the URLs come 100% from RepoSnapshot.URL and never from the model. The
// link tier is only assembled when opt.HasLinkCapable, and is left empty otherwise
// so no `[..](..)` is produced for text-only fan-outs.
func buildTrending(ctx context.Context, opt Options, d digest.Digest, lang i18n.OutputLanguage, limit int) (builtBody, error) {
	maxRepos := effectiveMaxReposPerSection(opt.Cfg)
	fallback := func() builtBody {
		text := ruleBasedTrending(lang, d, maxRepos)
		bb := builtBody{text: text, source: SourceFallback}
		if opt.HasLinkCapable {
			bb.bodyLink = ruleBasedTrendingLinks(lang, d, maxRepos)
		}
		return bb
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
	bb := builtBody{
		text:   summaryBody(summary),
		source: SourceModel,
		usage:  res.Usage,
		calls:  1,
	}
	if opt.HasLinkCapable {
		bb.bodyLink = trendingLinksBody(lang, d, maxRepos, summary)
	}
	return bb, nil
}

// buildReleases produces the releases body: try the model, degrade to rule-based
// on any failure. Never returns an error that would fail the run. Same one-call /
// two-tier scheme as buildTrending.
func buildReleases(ctx context.Context, opt Options, rel releasedigest.ReleaseDigest, lang i18n.OutputLanguage, limit int) (builtBody, error) {
	maxRepos := effectiveMaxReposPerSection(opt.Cfg)
	fallback := func() builtBody {
		text := ruleBasedReleases(lang, rel, maxRepos)
		bb := builtBody{text: text, source: SourceFallback}
		if opt.HasLinkCapable {
			bb.bodyLink = ruleBasedReleasesLinks(lang, rel, maxRepos)
		}
		return bb
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
	bb := builtBody{
		text:   summaryBody(summary),
		source: SourceModel,
		usage:  res.Usage,
		calls:  1,
	}
	if opt.HasLinkCapable {
		bb.bodyLink = releasesLinksBody(lang, rel, maxRepos, summary)
	}
	return bb, nil
}

// trendingLinksBody builds the link-capable tier body for a successful model run:
// the model summary (plain content) followed by a deterministic, link-annotated
// per-repo list. The list is the rule-based item list with allowLinks=true, so
// every URL comes straight from RepoSnapshot.URL. The model never sees these
// links; they are assembled here from the same single completion's output.
func trendingLinksBody(lang i18n.OutputLanguage, d digest.Digest, maxRepos int, summary string) string {
	list := ruleBasedTrendingLinks(lang, d, maxRepos)
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return list
	}
	if list == "" {
		return summary
	}
	if lang.ID == "en" {
		return summary + "\n\nRepositories:\n" + list
	}
	return summary + "\n\n仓库列表：\n" + list
}

// releasesLinksBody is the releases analogue of trendingLinksBody.
func releasesLinksBody(lang i18n.OutputLanguage, rel releasedigest.ReleaseDigest, maxRepos int, summary string) string {
	list := ruleBasedReleasesLinks(lang, rel, maxRepos)
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return list
	}
	if list == "" {
		return summary
	}
	if lang.ID == "en" {
		return summary + "\n\nRepositories:\n" + list
	}
	return summary + "\n\n仓库列表：\n" + list
}
