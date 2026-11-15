package notify

// channels.go builds golib gnotify notifiers from the resolved channel specs,
// fans a message out to them, and maps the per-channel results into the
// contract.NotifyReport shape. Truncation (§6.2) happens per channel just before
// send, because the byte ceiling differs per provider.
//
// NOTE on MultiSender: gnotify.MultiSender fans ONE identical Message to every
// notifier, but design §6.2 requires a DIFFERENT truncated body per channel
// (企微 4000 / 钉钉 18000 / 飞书 28000 bytes). So the fan-out here calls each
// notifier's Send directly with its own truncated body — the same primitive
// MultiSender uses internally — while honouring the configured strategy
// (broadcast = send all; firstSuccess = stop at first success; requireAll = same
// fan-out, error aggregated). When no truncation is needed every channel gets
// the same body, so this is behaviourally identical to a BroadcastAll broadcast.
//
// gnotify's channel constructors take a gnotify.HTTPDoer (its own small
// interface), not a *http.Client. Options.HTTPClient is a *http.Client (frozen
// signature, handy for tests), so httpDoerAdapter bridges the two.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/morehao/golib/gnotify"
	"github.com/morehao/golib/gnotify/dingtalk"
	"github.com/morehao/golib/gnotify/lark"
	"github.com/morehao/golib/gnotify/wecom"

	contract "github.com/morehao/trendradar/contract"
	"github.com/morehao/trendradar/internal/i18n"
)

// httpDoerAdapter adapts a std *http.Client to gnotify.HTTPDoer so tests (and
// callers) can inject a plain client. It never treats a >=400 status as an
// error: channel code interprets the status/errcode itself (gnotify contract).
type httpDoerAdapter struct{ c *http.Client }

// Do implements gnotify.HTTPDoer.
func (a httpDoerAdapter) Do(ctx context.Context, req gnotify.HTTPRequest) (gnotify.HTTPResponse, error) {
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}
	hreq, err := http.NewRequestWithContext(ctx, method, req.URL, body)
	if err != nil {
		return gnotify.HTTPResponse{}, err
	}
	for k, v := range req.Headers {
		hreq.Header.Set(k, v)
	}
	resp, err := a.c.Do(hreq)
	if err != nil {
		return gnotify.HTTPResponse{}, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return gnotify.HTTPResponse{}, err
	}
	return gnotify.HTTPResponse{StatusCode: resp.StatusCode, Body: respBody, Header: resp.Header}, nil
}

// buildNotifier constructs one gnotify.Notifier for a channel spec.
func buildNotifier(spec ChannelSpec, opts []gnotify.Option) (gnotify.Notifier, error) {
	switch spec.Name {
	case "dingtalk":
		return dingtalk.New(dingtalk.Config{WebhookURL: spec.WebhookURL, Secret: spec.Secret, MsgType: spec.MsgType}, opts...)
	case "lark":
		return lark.New(lark.Config{WebhookURL: spec.WebhookURL, Secret: spec.Secret}, opts...)
	case "wecom":
		return wecom.New(wecom.WecomConfig{WebhookURL: spec.WebhookURL}, opts...)
	default:
		return nil, errors.New("notify: unknown channel " + spec.Name)
	}
}

// msgTypeFor picks the per-channel msgtype to hand to gnotify via
// Params["msgtype"]. The channel constructors already default sensibly
// (dingtalk/wecom markdown, lark text); we pass the configured msgtype when set
// so an operator can override. lark's msgtype set is text|post, dingtalk/wecom
// text|markdown — config.validate already restricts these, so we pass as-is.
func msgTypeFor(spec ChannelSpec) string { return spec.MsgType }

// deliver is the real Deliver implementation (design §6.3: never fatal).
func deliver(ctx context.Context, opt Options, msgs []Message) contract.NotifyReport {
	rep := contract.NotifyReport{Enabled: true}
	if len(msgs) == 0 {
		return rep
	}
	rep.Attempted = true

	lang := resolveLang(opt.Lang)
	specs := channelSpecs(opt.Cfg)
	strategy := effectiveStrategy(opt.Cfg)
	timeout := time.Duration(effectiveTimeoutSeconds(opt.Cfg)) * time.Second
	maxBody := opt.Cfg.MaxBodyChars

	gopts := []gnotify.Option{gnotify.WithTimeout(timeout)}
	if opt.HTTPClient != nil {
		gopts = append(gopts, gnotify.WithHTTPDoer(httpDoerAdapter{c: opt.HTTPClient}))
	}

	var usageCalls, usageTokens int
	usageKnown := false

	for _, msg := range msgs {
		nm := contract.NotifyMessage{
			Kind:          msg.Kind,
			BodyChars:     runeLen(msg.Body),
			SummarySource: msg.summarySource,
		}
		if msg.Period != "" {
			p := msg.Period
			nm.Period = &p
		}
		usageCalls += msg.usageCalls
		usageTokens += msg.usage.TotalTokens
		if msg.usage.Known {
			usageKnown = true
		}
		nm.Channels = sendMessage(ctx, gopts, specs, strategy, msg, maxBody, lang)
		rep.Messages = append(rep.Messages, nm)
	}

	if usageCalls > 0 || usageKnown {
		rep.Usage = map[string]any{
			"calls":       usageCalls,
			"totalTokens": usageTokens,
			"usageKnown":  usageKnown,
		}
	}
	return rep
}

// sendMessage fans one message out to the configured channels per the strategy,
// applying per-channel truncation, and returns the ordered contract results
// (one per configured channel; disabled ones as status="disabled").
func sendMessage(ctx context.Context, gopts []gnotify.Option, specs []ChannelSpec,
	strategy string, msg Message, maxBody int, lang i18n.OutputLanguage) []contract.NotifyChannelResult {
	out := make([]contract.NotifyChannelResult, 0, len(specs))

	for _, spec := range specs {
		if !spec.Enabled || spec.WebhookURL == "" {
			out = append(out, contract.NotifyChannelResult{Channel: spec.Name, Status: "disabled"})
			continue
		}
		res := contract.NotifyChannelResult{Channel: spec.Name}
		notifier, err := buildNotifier(spec, gopts)
		if err != nil {
			res.Status = "failed"
			res.Error = errPtr(err.Error())
			out = append(out, res)
			continue
		}

		body := truncateForChannel(msg.Body, spec.Name, maxBody, lang)
		gmsg := gnotify.Message{Subject: msg.Subject, Body: body, Params: map[string]string{"msgtype": msgTypeFor(spec)}}

		start := time.Now()
		sendErr := notifier.Send(ctx, gmsg)
		ms := int(time.Since(start) / time.Millisecond)
		res.DurationMs = &ms
		if sendErr != nil {
			res.Status = "failed"
			res.Error = errPtr(sendErr.Error())
		} else {
			res.Status = "sent"
		}
		out = append(out, res)

		// FirstSuccess: stop at the first successful channel (degrade semantics).
		if strategy == StrategyFirstSuccess && sendErr == nil {
			break
		}
	}
	return out
}

// errPtr returns a pointer to s (never nil so an empty error string is distinct
// from a nil error field, matching the "failed must carry error" contract).
func errPtr(s string) *string { return &s }
