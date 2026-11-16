package notify

// This file adapts config.NotifyConfig (WS-A, internal/config) into the small
// internal view the notify package uses. The frozen §1.3 signature types
// Options.Cfg as config.NotifyConfig directly; this file holds the read helpers
// and the channel-spec projection so the rest of the package never touches the
// raw config struct, keeping the surface small and the behaviour defensive
// (defaults are re-applied here too, so Build/Deliver/tests behave the same
// whether or not config defaults were already filled in).
//
// Field names mirror design §1.1 exactly:
//   NotifyConfig{Enabled *bool, Sections NotifySections{*bool}, OnSkipped,
//   OnEmptyReleases, Strategy, Channels NotifyChannels{DingTalk,Lark,WeCom},
//   MaxReposPerSection, MaxBodyChars, TimeoutSeconds, SummaryMaxChars}
// with IsEnabled()/TrendingEnabled()/ReleasesEnabled() accessors.

import "github.com/morehao/trendradar/internal/config"

// Defaults mirror design §1.1 example values, re-applied defensively here.
const (
	DefaultMaxReposPerSection = 10
	DefaultTimeoutSeconds     = 15
	DefaultSummaryMaxChars    = 800

	// StrategyBroadcast sends to all enabled channels best-effort (default).
	StrategyBroadcast = "broadcast"
	// StrategyRequireAll requires every channel to succeed (semantics only).
	StrategyRequireAll = "requireAll"
	// StrategyFirstSuccess falls back channel-by-channel, first success wins.
	StrategyFirstSuccess = "firstSuccess"
)

// sectionMessageKinds are the message kinds.
const (
	KindTrending = "trending"
	KindReleases = "releases"
)

// Summary sources recorded on a message.
const (
	SourceModel    = "model"
	SourceFallback = "fallback"
)

// effectiveSummaryMaxChars returns SummaryMaxChars, defaulting when unset.
func effectiveSummaryMaxChars(c config.NotifyConfig) int {
	if c.SummaryMaxChars <= 0 {
		return DefaultSummaryMaxChars
	}
	return c.SummaryMaxChars
}

// effectiveMaxReposPerSection returns MaxReposPerSection, defaulting when unset.
func effectiveMaxReposPerSection(c config.NotifyConfig) int {
	if c.MaxReposPerSection <= 0 {
		return DefaultMaxReposPerSection
	}
	return c.MaxReposPerSection
}

// effectiveTimeoutSeconds returns TimeoutSeconds, defaulting when unset.
func effectiveTimeoutSeconds(c config.NotifyConfig) int {
	if c.TimeoutSeconds <= 0 {
		return DefaultTimeoutSeconds
	}
	return c.TimeoutSeconds
}

// effectiveStrategy returns the normalized delivery strategy (default broadcast).
func effectiveStrategy(c config.NotifyConfig) string {
	switch c.Strategy {
	case StrategyBroadcast, StrategyRequireAll, StrategyFirstSuccess:
		return c.Strategy
	default:
		return StrategyBroadcast
	}
}

// ChannelSpec is the deliverable form of one channel (name + resolved config),
// independent of which concrete gnotify notifier builds it.
type ChannelSpec struct {
	Name       string // "dingtalk" | "lark" | "wecom"
	Enabled    bool
	WebhookURL string
	Secret     string
	MsgType    string
}

// channelSpecs returns every configured channel spec in a stable order
// (dingtalk, lark, wecom). All channels are returned (enabled or not) so Deliver
// can record disabled ones as status="disabled"; enabled ones are actually sent.
func channelSpecs(c config.NotifyConfig) []ChannelSpec {
	return []ChannelSpec{
		{Name: "dingtalk", Enabled: c.Channels.DingTalk.Enabled, WebhookURL: c.Channels.DingTalk.WebhookURL, Secret: c.Channels.DingTalk.Secret, MsgType: c.Channels.DingTalk.MsgType},
		{Name: "lark", Enabled: c.Channels.Lark.Enabled, WebhookURL: c.Channels.Lark.WebhookURL, Secret: c.Channels.Lark.Secret, MsgType: c.Channels.Lark.MsgType},
		{Name: "wecom", Enabled: c.Channels.WeCom.Enabled, WebhookURL: c.Channels.WeCom.WebhookURL, Secret: c.Channels.WeCom.Secret, MsgType: c.Channels.WeCom.MsgType},
	}
}
