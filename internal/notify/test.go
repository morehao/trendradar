package notify

import (
	"context"

	contract "github.com/morehao/trendradar/contract"
	"github.com/morehao/trendradar/internal/config"
	"github.com/morehao/trendradar/internal/i18n"
)

// TestChannelAll is the sentinel accepted by TestDeliver to mean "every enabled
// channel" (equivalent to passing an empty channel). Exposed so callers need not
// hard-code the empty string.
const TestChannelAll = ""

// TestBody is the fixed body sent by a connectivity test. It is intentionally
// short, clearly a test, and never derived from real content.
const (
	testSubjectZh = "【webhook 测试】"
	testSubjectEn = "[webhook test]"
	testBodyZh    = "这是一条 webhook 连通性测试消息，收到即表示该渠道配置可用。可忽略。"
	testBodyEn    = "This is a webhook connectivity test message. Receiving it means this channel is configured correctly. You can ignore it."
)

// testSubject/testBody pick a subject/body for the resolved language. Kept
// minimal on purpose: a test message should look obviously like a test.
func testSubject(lang i18n.OutputLanguage) string {
	if lang.ID == "en" {
		return testSubjectEn
	}
	return testSubjectZh
}

func testBody(lang i18n.OutputLanguage) string {
	if lang.ID == "en" {
		return testBodyEn
	}
	return testBodyZh
}

// TestDeliver sends ONE fixed test message to the requested channel (or every
// enabled channel when channel is empty / TestChannelAll) and returns the
// contract report — the exact same shape a real run produces, so the plugin can
// render per-channel status/error uniformly.
//
// It reuses Deliver, so signing, msgtype, truncation, strategy, timeout and the
// ${VAR}-expanded webhook/secret are all identical to a real push. It never
// touches the network for anything except the configured webhooks.
//
// When a specific channel is named, only that channel is considered enabled for
// this call (a disabled channel is still attempted, because "test this channel"
// means "can this webhook send", regardless of the enabled switch). Other
// channels are dropped so a test never fans out to unintended targets.
func TestDeliver(ctx context.Context, opt Options, channel string) contract.NotifyReport {
	cfg := opt.Cfg

	if channel != "" && channel != TestChannelAll {
		cfg = isolateChannel(cfg, channel)
	}

	lang := resolveLang(opt.Lang)
	msg := Message{
		Kind:    KindTrending,
		Period:  "",
		Subject: testSubject(lang),
		Body:    testBody(lang),
		// A test message is not model-generated; mark it as fallback so the
		// contract's required summarySource enum ("model"|"fallback") is valid.
		summarySource: SourceFallback,
	}
	testOpt := opt
	testOpt.Cfg = cfg
	return Deliver(ctx, testOpt, []Message{msg})
}

// isolateChannel returns a copy of cfg where only `channel` is enabled and
// carries a webhook. Unknown channel names leave cfg unchanged (Deliver then
// reports every configured channel; callers validate names beforehand).
func isolateChannel(cfg config.NotifyConfig, channel string) config.NotifyConfig {
	off := func(c *config.NotifyChannel) { c.Enabled = false }

	off(&cfg.Channels.DingTalk)
	off(&cfg.Channels.Lark)
	off(&cfg.Channels.WeCom)

	switch channel {
	case "dingtalk":
		cfg.Channels.DingTalk.Enabled = true
	case "lark":
		cfg.Channels.Lark.Enabled = true
	case "wecom":
		cfg.Channels.WeCom.Enabled = true
	}
	return cfg
}
