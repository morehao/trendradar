// Package notify condenses a run's structured output (the trending digest and
// the release-subscription digest) into at most two short markdown messages and
// delivers them to chat webhooks (DingTalk / Lark / WeCom) via golib gnotify.
//
// It is deliberately separate from render (documents for the data repo) and
// releasewatch (per-repo weekly summaries): a chat message must be short,
// scannable and split per section, a different audience and shape. The model
// call reuses the existing model.Provider (with its fallback chain); when the
// model is unavailable (or a Fake offline provider is used) Build degrades to a
// rule-based summary so a push still goes out — matching the "summary degrade
// never fails the run" policy used elsewhere.
//
// Delivery is best-effort: Deliver never returns a fatal error. A per-channel
// failure is recorded in the contract.NotifyReport result, and a failed push
// must never turn a successful run into a failed one (the caller app.Run keeps
// that contract).
package notify

import (
	"context"
	"net/http"

	contract "github.com/morehao/trendradar/contract"
	"github.com/morehao/trendradar/internal/config"
	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/i18n"
	"github.com/morehao/trendradar/internal/model"
	"github.com/morehao/trendradar/internal/releasedigest"
)

// Message is one outbound chat message (one section). Kind is "trending" or
// "releases". Period is the trending period (daily/weekly/monthly) and empty
// for the releases section. Subject is used as the message title / first line;
// Body is the markdown content produced by Build.
type Message struct {
	Kind    string
	Period  string
	Subject string
	Body    string

	// summarySource and usage are carried internally from Build to Deliver so
	// Deliver can fill contract.NotifyMessage without re-running the model.
	summarySource string
	usage         model.Usage
	usageCalls    int
}

// Options carries the inputs Build/Deliver need (frozen §1.3 signature).
type Options struct {
	Provider   model.Provider      // nil → Build degrades to rule-based summary
	Lang       string              // i18n output language (empty → zh)
	Cfg        config.NotifyConfig // frozen §1.1 config shape
	HTTPClient *http.Client        // injected into gnotify; nil → per-request default
}

// Build generates the messages to send for this run (at most two: one trending,
// one releases) and does not send anything. It is a pure function of its inputs
// except for the optional model call. wantTrending/wantReleases gate each
// section; the caller decides them from the run's result and the config gates.
func Build(ctx context.Context, opt Options, d digest.Digest, rel releasedigest.ReleaseDigest,
	wantTrending, wantReleases bool) ([]Message, error) {
	lang := resolveLang(opt.Lang)
	limit := effectiveSummaryMaxChars(opt.Cfg)

	var msgs []Message

	if wantTrending && len(d.Repos) > 0 {
		body, err := buildTrending(ctx, opt, d, lang, limit)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, Message{
			Kind:          KindTrending,
			Period:        d.Period,
			Subject:       trendingSubject(lang, d.Date, d.Period),
			Body:          body.text,
			summarySource: body.source,
			usage:         body.usage,
			usageCalls:    body.calls,
		})
	}

	if wantReleases && hasReleaseChanges(rel) {
		body, err := buildReleases(ctx, opt, rel, lang, limit)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, Message{
			Kind:          KindReleases,
			Subject:       releasesSubject(lang, rel),
			Body:          body.text,
			summarySource: body.source,
			usage:         body.usage,
			usageCalls:    body.calls,
		})
	}

	return msgs, nil
}

// Deliver sends each message to every enabled channel and returns the contract
// report. It never returns a fatal error: channel failures show up as
// status="failed" on the affected channel result. Disabled channels are recorded
// as status="disabled".
func Deliver(ctx context.Context, opt Options, msgs []Message) contract.NotifyReport {
	return deliver(ctx, opt, msgs)
}

// resolveLang returns a registered i18n language, defaulting to zh on any
// unknown/empty id (Build must not fail because of a language string).
func resolveLang(id string) i18n.OutputLanguage {
	if l, err := i18n.Resolve(id); err == nil {
		return l
	}
	return i18n.MustResolve(i18n.DefaultLanguage)
}

// hasReleaseChanges reports whether rel has at least one repo with NewCount > 0
// that will be summarized (releases message trigger, design §5.1 / §4).
func hasReleaseChanges(rel releasedigest.ReleaseDigest) bool {
	for _, r := range rel.Repos {
		if r.NewCount > 0 {
			return true
		}
	}
	return false
}
