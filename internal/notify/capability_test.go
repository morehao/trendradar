package notify

import "testing"

// TestSupportsMarkdownLink locks the channel × msgtype → link-capability table
// (design decision 5). This is the single source of truth, so a change here that
// flips text→true or markdown→false must be deliberate and accompanied by a
// render-behaviour change.
func TestSupportsMarkdownLink(t *testing.T) {
	cases := []struct {
		channel string
		msgType string
		want    bool
	}{
		{"dingtalk", "markdown", true},
		{"dingtalk", "text", false},
		{"wecom", "markdown", true},
		{"wecom", "text", false},
		{"lark", "interactive", true},
		{"lark", "post", false},
		{"lark", "text", false},
		// Unknown / unset msgtype falls to the safe no-link tier.
		{"lark", "", false},
		{"dingtalk", "unknown", false},
	}
	for _, c := range cases {
		spec := ChannelSpec{Name: c.channel, MsgType: c.msgType, Enabled: true, WebhookURL: "http://x"}
		if got := supportsMarkdownLink(spec); got != c.want {
			t.Errorf("supportsMarkdownLink(%s/%q) = %v, want %v", c.channel, c.msgType, got, c.want)
		}
	}
}

// TestLinkCapability verifies the enabled-channel union: any enabled link-capable
// channel turns it on; disabled or webhook-less channels do not count.
func TestLinkCapability(t *testing.T) {
	linkMD := ChannelSpec{Name: "dingtalk", MsgType: "markdown", Enabled: true, WebhookURL: "http://x"}
	linkLark := ChannelSpec{Name: "lark", MsgType: "interactive", Enabled: true, WebhookURL: "http://x"}
	plainText := ChannelSpec{Name: "wecom", MsgType: "text", Enabled: true, WebhookURL: "http://x"}
	postLark := ChannelSpec{Name: "lark", MsgType: "post", Enabled: true, WebhookURL: "http://x"}
	disabledMD := ChannelSpec{Name: "dingtalk", MsgType: "markdown", Enabled: false, WebhookURL: "http://x"}
	noWebhookMD := ChannelSpec{Name: "dingtalk", MsgType: "markdown", Enabled: true, WebhookURL: ""}

	cases := []struct {
		name  string
		specs []ChannelSpec
		want  bool
	}{
		{"empty", nil, false},
		{"only text", []ChannelSpec{plainText}, false},
		{"only post", []ChannelSpec{postLark}, false},
		{"markdown", []ChannelSpec{linkMD}, true},
		{"lark interactive", []ChannelSpec{linkLark}, true},
		{"text + markdown", []ChannelSpec{plainText, linkMD}, true},
		{"post + text", []ChannelSpec{postLark, plainText}, false},
		{"disabled markdown only", []ChannelSpec{disabledMD}, false},
		{"webhook-less markdown only", []ChannelSpec{noWebhookMD}, false},
	}
	for _, c := range cases {
		if got := linkCapability(c.specs); got != c.want {
			t.Errorf("linkCapability(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}
