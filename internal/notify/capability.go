package notify

// capability.go models the render capability of a channel as a function of its
// (channel, msgtype). It is the single source of truth for "can this channel
// render markdown links `[text](url)`?", used in two places:
//
//   - Build (via Options.HasLinkCapable, computed from all enabled specs) decides
//     whether a link-tier body (Message.BodyLink) is produced at all;
//   - sendMessage picks the link tier for a channel that supports markdown links
//     and the plain tier otherwise.
//
// The capability is a static rendering property of the protocol, not a
// user-facing setting: it is NOT exposed on NotifyConfig (design decision 5).
// If a deployment's channel renders differently, that is expressed by switching
// the channel's msgtype to `text` (the existing escape hatch), not by a new field.

// supportsMarkdownLink reports whether the given channel spec can render a
// markdown hyperlink `[text](url)`. The mapping is the only place this decision
// is made:
//
//	markdown    → true   (dingtalk/wecom markdown)
//	interactive → true   (lark interactive card: the body is a lark_md node, which
//	                      renders headings/bold/lists/links)
//	post        → false  (lark post puts the whole body into a SINGLE text node
//	                      {"tag":"text","text":content} that does NOT parse
//	                      markdown — `[owner/repo](url)` would show up literally and
//	                      not be clickable; see golib v1.38.3 gnotify/lark/lark.go
//	                      msgTypePost branch and internal/config/config.go:604)
//	text        → false  (all channels' plain text: `[..](..)` would show literally)
//
// Any msgtype outside the true set — `text`, `post`, unset, or unknown — falls to
// false (the safe, no-`[]()` tier). Channel constructors default to a markdown/
// interactive msgtype, so an empty msgtype here only means "not explicitly set".
func supportsMarkdownLink(spec ChannelSpec) bool {
	switch spec.MsgType {
	case "markdown", "interactive":
		return true
	default: // "post", "text" and anything unrecognized
		return false
	}
}

// linkCapability summarizes the link capability across all enabled channel specs:
// it reports whether at least one enabled channel can render markdown links. Build
// uses this to decide whether to assemble the link tier body at all. When no
// channel is enabled it returns false (nothing to send, so no link tier needed).
func linkCapability(specs []ChannelSpec) bool {
	for _, s := range specs {
		if s.Enabled && s.WebhookURL != "" && supportsMarkdownLink(s) {
			return true
		}
	}
	return false
}
