package config

import (
	"errors"
	"strings"
	"testing"
)

// baseYAML is a minimal valid config so notify blocks can be dropped in and
// Load/Validate exercised end-to-end (including env expansion + defaults).
const notifyBaseYAML = `
model:
  provider: deepseek
  name: deepseek-chat
  baseURL: https://api.deepseek.com/v1
  apiKey: test-key
repo:
  path: /tmp/repo
trending:
  languages: [all]
  since: daily
output:
  dir: digests
`

func TestNotifyDefaults(t *testing.T) {
	// No notify block at all -> disabled, with documented defaults filled in.
	c, err := Load(writeTemp(t, notifyBaseYAML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	n := c.Notify
	if n.IsEnabled() {
		t.Fatal("notify must default to disabled")
	}
	if !n.Sections.TrendingEnabled() || !n.Sections.ReleasesEnabled() {
		t.Fatalf("sections must default both true: %+v", n.Sections)
	}
	if n.Strategy != "broadcast" {
		t.Fatalf("strategy default = %q, want broadcast", n.Strategy)
	}
	if n.MaxReposPerSection != 10 || n.MaxBodyChars != 0 || n.TimeoutSeconds != 15 || n.SummaryMaxChars != 800 {
		t.Fatalf("numeric defaults wrong: %+v", n)
	}
	if n.Channels.DingTalk.MsgType != "markdown" || n.Channels.Lark.MsgType != "interactive" || n.Channels.WeCom.MsgType != "markdown" {
		t.Fatalf("msgtype defaults wrong: %+v", n.Channels)
	}
}

func TestNotifyValidMatrix(t *testing.T) {
	cases := map[string]string{
		"dingtalk only": `
notify:
  enabled: true
  channels:
    dingtalk:
      enabled: true
      webhookURL: https://oapi.dingtalk.com/robot/send?access_token=abc
`,
		"lark only with secret": `
notify:
  enabled: true
  channels:
    lark:
      enabled: true
      webhookURL: https://open.feishu.cn/open-apis/bot/v2/hook/abc
      secret: s3cr3t
`,
		"wecom only": `
notify:
  enabled: true
  channels:
    wecom:
      enabled: true
      webhookURL: https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=abc
`,
		"single section + explicit limits": `
notify:
  enabled: true
  sections:
    trending: true
    releases: false
  maxBodyChars: 500
  channels:
    dingtalk:
      enabled: true
      webhookURL: https://example.com/hook
      msgtype: text
`,
		"all channels, all defaults": `
notify:
  enabled: true
  channels:
    dingtalk:
      enabled: true
      webhookURL: https://example.com/d
    lark:
      enabled: true
      webhookURL: https://example.com/l
    wecom:
      enabled: true
      webhookURL: https://example.com/w
`,
	}
	for name, block := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeTemp(t, notifyBaseYAML+block)); err != nil {
				t.Fatalf("expected valid, got: %v", err)
			}
		})
	}
}

func TestNotifyInvalidMatrix(t *testing.T) {
	cases := map[string]struct {
		block  string
		errSub string // substring that must appear in the error
	}{
		"enabled but no section": {
			block: `
notify:
  enabled: true
  sections:
    trending: false
    releases: false
  channels:
    dingtalk:
      enabled: true
      webhookURL: https://example.com/d
`,
			errSub: "at least one of trending/releases",
		},
		"enabled but no channel": {
			block: `
notify:
  enabled: true
  channels:
    dingtalk:
      enabled: false
      webhookURL: https://example.com/d
`,
			errSub: "at least one channel must be enabled",
		},
		"enabled channel empty webhook": {
			block: `
notify:
  enabled: true
  channels:
    dingtalk:
      enabled: true
      webhookURL: ""
`,
			errSub: "webhookURL: required",
		},
		"unresolved VAR in webhook becomes empty": {
			// Through Load, os.ExpandEnv turns an unset ${VAR} into "", so this
			// surfaces as the "required" error — still CONFIG_INVALID, still
			// without echoing the value. The literal-${ branch is covered by
			// TestNotifyValidateUnresolvedPlaceholder via a direct struct.
			block: `
notify:
  enabled: true
  channels:
    lark:
      enabled: true
      webhookURL: ${NOTIFY_TEST_UNSET_HOOK}
`,
			errSub: "webhookURL: required",
		},
		"bad msgtype dingtalk": {
			block: `
notify:
  enabled: true
  channels:
    dingtalk:
      enabled: true
      webhookURL: https://example.com/d
      msgtype: post
`,
			errSub: "msgtype",
		},
		"bad msgtype wecom": {
			block: `
notify:
  enabled: true
  channels:
    wecom:
      enabled: true
      webhookURL: https://example.com/w
      msgtype: post
`,
			errSub: "msgtype",
		},
		"bad strategy": {
			block: `
notify:
  enabled: true
  strategy: roundrobin
  channels:
    dingtalk:
      enabled: true
      webhookURL: https://example.com/d
`,
			errSub: "strategy",
		},
		"maxBodyChars below range": {
			block: `
notify:
  enabled: true
  maxBodyChars: 199
  channels:
    dingtalk:
      enabled: true
      webhookURL: https://example.com/d
`,
			errSub: "maxBodyChars",
		},
		"maxBodyChars above range": {
			block: `
notify:
  enabled: true
  maxBodyChars: 20001
  channels:
    dingtalk:
      enabled: true
      webhookURL: https://example.com/d
`,
			errSub: "maxBodyChars",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeTemp(t, notifyBaseYAML+tc.block))
			if err == nil {
				t.Fatal("expected CONFIG_INVALID, got nil")
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("error must wrap ErrInvalid (CONFIG_INVALID), got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.errSub) {
				t.Fatalf("error %q must contain %q", err.Error(), tc.errSub)
			}
		})
	}
}

// TestNotifyValidateUnresolvedPlaceholder calls validate() directly on a struct
// whose webhook/secret still contains a literal ${VAR} (the state a caller would
// be in if it built the config without running expandEnv). It proves the
// "unresolved ${VAR}" branch fires and never echoes the value.
func TestNotifyValidateUnresolvedPlaceholder(t *testing.T) {
	tr := true
	base := func() NotifyConfig {
		return NotifyConfig{
			Enabled:  &tr,
			Strategy: "broadcast",
			Channels: NotifyChannels{
				DingTalk: NotifyChannel{Enabled: true, WebhookURL: "https://example.com/d", MsgType: "markdown"},
			},
		}
	}

	t.Run("webhook literal placeholder", func(t *testing.T) {
		n := base()
		n.Channels.DingTalk.WebhookURL = "https://example.com/${SOME_SECRET}"
		err := n.validate()
		if err == nil || !strings.Contains(err.Error(), "unresolved ${VAR}") {
			t.Fatalf("want unresolved ${VAR}, got: %v", err)
		}
		if strings.Contains(err.Error(), "${SOME_SECRET}") {
			t.Fatalf("error must not echo the raw placeholder/value: %v", err)
		}
	})

	t.Run("secret literal placeholder", func(t *testing.T) {
		n := base()
		n.Channels.DingTalk.Secret = "${SOME_SECRET}"
		err := n.validate()
		if err == nil || !strings.Contains(err.Error(), "secret: unresolved ${VAR}") {
			t.Fatalf("want secret unresolved ${VAR}, got: %v", err)
		}
		if strings.Contains(err.Error(), "SOME_SECRET") {
			t.Fatalf("error must not echo the secret placeholder/value: %v", err)
		}
	})

	t.Run("empty secret is allowed", func(t *testing.T) {
		n := base()
		n.Channels.DingTalk.Secret = ""
		if err := n.validate(); err != nil {
			t.Fatalf("empty secret must be valid: %v", err)
		}
	})
}

// TestNotifyDisabledSkipsValidation: an invalid-looking but disabled block must
// still load, because the whole stage is bypassed when disabled.
func TestNotifyDisabledSkipsValidation(t *testing.T) {
	body := notifyBaseYAML + `
notify:
  enabled: false
  sections:
    trending: false
    releases: false
  strategy: whatever
  maxBodyChars: 5
  channels:
    dingtalk:
      enabled: true
      webhookURL: ""
`
	if _, err := Load(writeTemp(t, body)); err != nil {
		t.Fatalf("disabled notify must not be validated, got: %v", err)
	}
}

func TestNotifyDefaultsApplyOnEnabledOnly(t *testing.T) {
	// `enabled: true` plus one channel is a complete config once defaults fill
	// sections/strategy/limits/msgtype.
	body := notifyBaseYAML + `
notify:
  enabled: true
  channels:
    wecom:
      enabled: true
      webhookURL: https://example.com/w
`
	c, err := Load(writeTemp(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.Notify.Sections.TrendingEnabled() || !c.Notify.Sections.ReleasesEnabled() {
		t.Fatalf("sections default not applied: %+v", c.Notify.Sections)
	}
	if c.Notify.Channels.WeCom.MsgType != "markdown" {
		t.Fatalf("wecom msgtype default not applied: %q", c.Notify.Channels.WeCom.MsgType)
	}
}
