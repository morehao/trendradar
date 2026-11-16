package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	contract "github.com/morehao/trendradar/contract"
	"github.com/morehao/trendradar/internal/config"
)

// countingWebhook returns a 200 success webhook that increments hits per request.
func countingWebhook(t *testing.T, hits *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","code":0,"msg":"success"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestTestDeliver_AllChannels verifies TestDeliver sends one fixed message and
// reports every enabled channel (mock webhook returns success for all).
func TestTestDeliver_AllChannels(t *testing.T) {
	var hits int32
	srv := countingWebhook(t, &hits)
	opt := Options{Lang: "zh", Cfg: allEnabled(srv.URL), HTTPClient: srv.Client()}

	rep := TestDeliver(context.Background(), opt, TestChannelAll)
	if !rep.Attempted {
		t.Fatal("expected attempted=true")
	}
	if len(rep.Messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(rep.Messages))
	}
	st := chanStatus(rep, 0)
	for _, name := range []string{"dingtalk", "lark", "wecom"} {
		if st[name] != "sent" {
			t.Errorf("%s status = %q, want sent", name, st[name])
		}
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("webhook hits = %d, want 3 (one per channel)", got)
	}
}

// TestTestDeliver_IsolatesChannel verifies --channel sends to ONLY that channel,
// even when the other channels are enabled with a working webhook.
func TestTestDeliver_IsolatesChannel(t *testing.T) {
	var hits int32
	srv := countingWebhook(t, &hits)
	opt := Options{Lang: "zh", Cfg: allEnabled(srv.URL), HTTPClient: srv.Client()}

	rep := TestDeliver(context.Background(), opt, "lark")
	st := chanStatus(rep, 0)
	if st["lark"] != "sent" {
		t.Errorf("lark status = %q, want sent", st["lark"])
	}
	if st["dingtalk"] != "disabled" || st["wecom"] != "disabled" {
		t.Errorf("other channels should be disabled, got dingtalk=%q wecom=%q", st["dingtalk"], st["wecom"])
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("webhook hits = %d, want 1 (only lark)", got)
	}
}

// TestTestDeliver_ChannelDisabledStillTested: --channel tests the webhook even
// if the channel's Enabled switch is off (a webhook test is about reachability,
// not the on/off toggle).
func TestTestDeliver_ChannelDisabledStillTested(t *testing.T) {
	var hits int32
	srv := countingWebhook(t, &hits)
	cfg := baseConfig()
	cfg.Channels.Lark = config.NotifyChannel{Enabled: false, WebhookURL: srv.URL, MsgType: "text"}
	opt := Options{Lang: "zh", Cfg: cfg, HTTPClient: srv.Client()}

	rep := TestDeliver(context.Background(), opt, "lark")
	st := chanStatus(rep, 0)
	if st["lark"] != "sent" {
		t.Errorf("lark status = %q, want sent (test ignores enabled switch)", st["lark"])
	}
}

// TestTestDeliver_FailureCarriesError verifies a failing webhook yields
// status=failed with a non-nil, non-empty error string.
func TestTestDeliver_FailureCarriesError(t *testing.T) {
	srv := fakeWebhook(t, 200, `{"errcode":93000,"errmsg":"invalid webhook","code":93000,"msg":"bad"}`, 0)
	cfg := baseConfig()
	cfg.Channels.Lark = config.NotifyChannel{Enabled: true, WebhookURL: srv.URL, MsgType: "text"}
	opt := Options{Lang: "zh", Cfg: cfg, HTTPClient: srv.Client()}

	rep := TestDeliver(context.Background(), opt, "lark")
	if len(rep.Messages) != 1 {
		t.Fatalf("unexpected report shape: %+v", rep)
	}
	var c *contract.NotifyChannelResult
	for i := range rep.Messages[0].Channels {
		if rep.Messages[0].Channels[i].Channel == "lark" {
			c = &rep.Messages[0].Channels[i]
		}
	}
	if c == nil {
		t.Fatal("lark channel missing from report")
	}
	if c.Status != "failed" {
		t.Fatalf("status = %q, want failed", c.Status)
	}
	if c.Error == nil || *c.Error == "" {
		t.Fatal("failed channel must carry a non-empty error")
	}
}

// TestTestDeliver_MessageIsMarkedFallback: a test message has no model summary,
// so summarySource must satisfy the contract enum ("fallback"), never empty.
func TestTestDeliver_MessageIsMarkedFallback(t *testing.T) {
	var hits int32
	srv := countingWebhook(t, &hits)
	opt := Options{Lang: "zh", Cfg: allEnabled(srv.URL), HTTPClient: srv.Client()}
	rep := TestDeliver(context.Background(), opt, "lark")
	if got := rep.Messages[0].SummarySource; got != "fallback" {
		t.Fatalf("summarySource = %q, want fallback", got)
	}
}
