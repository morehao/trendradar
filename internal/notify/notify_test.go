package notify

// Offline unit tests: Build (Top-N / fallback / truncation / want switches) and
// Deliver (three fake webhooks via httptest: errcode=0→sent, errcode!=0→failed,
// 5xx→failed, timeout→failed, disabled→disabled). No network, no real secrets:
// model.NewFake() drives the "model" path; nil / a local error provider drives
// the "fallback" path.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	contract "github.com/morehao/trendradar/contract"
	"github.com/morehao/trendradar/internal/config"
	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/model"
	"github.com/morehao/trendradar/internal/releasedigest"
)

// ---- fixtures -------------------------------------------------------------

func strptr(s string) *string { return &s }

func trendingDigest(period string, n int) digest.Digest {
	repos := make([]digest.RepoDigest, 0, n)
	for i := 0; i < n; i++ {
		repos = append(repos, digest.RepoDigest{
			RepoSnapshot: digest.RepoSnapshot{
				FullName:      fmt.Sprintf("owner%d/repo%d", i, i),
				StarsInPeriod: (i + 1) * 100,
				Rank:          i + 1,
				Description:   strptr(fmt.Sprintf("desc %d", i)),
			},
			Summary: fmt.Sprintf("summary %d", i),
		})
	}
	return digest.Digest{Schema: 3, Date: "2026-11-15", Period: period, Count: n, Repos: repos}
}

func releaseDigest(repos ...releasedigest.RepoReleases) releasedigest.ReleaseDigest {
	return releasedigest.ReleaseDigest{Schema: 2, Date: "2026-11-15", Period: "weekly", Count: len(repos), Repos: repos}
}

func repoReleases(name string, newCount int, latest, summary string) releasedigest.RepoReleases {
	r := releasedigest.RepoReleases{FullName: name, NewCount: newCount, LatestTag: latest}
	if summary != "" {
		r.Summary = strptr(summary)
	}
	return r
}

// errProvider always fails, to exercise the fallback path deterministically.
type errProvider struct{}

func (errProvider) Name() string { return "err" }
func (errProvider) Complete(context.Context, string, string) (model.Result, error) {
	return model.Result{}, fmt.Errorf("boom")
}

// degradedProvider signals a provider-side fallback.
type degradedProvider struct{}

func (degradedProvider) Name() string { return "degraded" }
func (degradedProvider) Complete(context.Context, string, string) (model.Result, error) {
	return model.Result{Degraded: true, Text: "ignored"}, nil
}

func baseConfig() config.NotifyConfig {
	return config.NotifyConfig{
		Sections:           config.NotifySections{},
		SummaryMaxChars:    800,
		MaxReposPerSection: 10,
		TimeoutSeconds:     15,
		Strategy:           "broadcast",
	}
}

// ---- Build ----------------------------------------------------------------

func TestBuild_TopNAndRest(t *testing.T) {
	cfg := baseConfig()
	cfg.MaxReposPerSection = 3
	opt := Options{Provider: nil, Lang: "zh", Cfg: cfg}
	d := trendingDigest("daily", 12)

	msgs, err := Build(context.Background(), opt, d, releasedigest.ReleaseDigest{}, true, false)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1 (trending)", len(msgs))
	}
	m := msgs[0]
	if m.Kind != KindTrending || m.Period != "daily" {
		t.Fatalf("kind/period = %q/%q", m.Kind, m.Period)
	}
	if m.summarySource != SourceFallback {
		t.Fatalf("summarySource = %q, want fallback (nil provider)", m.summarySource)
	}
	// Top-3 named, remaining 9 reported.
	for i := 0; i < 3; i++ {
		if !strings.Contains(m.Body, fmt.Sprintf("owner%d/repo%d", i, i)) {
			t.Errorf("body missing top repo %d:\n%s", i, m.Body)
		}
	}
	if strings.Contains(m.Body, "owner3/repo3") {
		t.Errorf("body leaked 4th repo beyond Top-N:\n%s", m.Body)
	}
	if !strings.Contains(m.Body, "其余 9 个") {
		t.Errorf("body missing rest note:\n%s", m.Body)
	}
}

func TestBuild_WantSwitch(t *testing.T) {
	opt := Options{Provider: nil, Lang: "zh", Cfg: baseConfig()}
	d := trendingDigest("daily", 3)
	rel := releaseDigest(repoReleases("a/b", 2, "v1.2.0", "some summary"))

	// Only trending wanted.
	msgs, _ := Build(context.Background(), opt, d, rel, true, false)
	if len(msgs) != 1 || msgs[0].Kind != KindTrending {
		t.Fatalf("want only trending, got %+v", msgs)
	}
	// Only releases wanted.
	msgs, _ = Build(context.Background(), opt, d, rel, false, true)
	if len(msgs) != 1 || msgs[0].Kind != KindReleases {
		t.Fatalf("want only releases, got %+v", msgs)
	}
	// Both.
	msgs, _ = Build(context.Background(), opt, d, rel, true, true)
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages, got %d", len(msgs))
	}
	// Neither.
	msgs, _ = Build(context.Background(), opt, d, rel, false, false)
	if len(msgs) != 0 {
		t.Fatalf("want 0 messages, got %d", len(msgs))
	}
}

func TestBuild_ReleasesOnlyChanged(t *testing.T) {
	opt := Options{Provider: nil, Lang: "zh", Cfg: baseConfig()}
	// No repo has NewCount>0 → no releases message even when wanted.
	rel := releaseDigest(repoReleases("a/b", 0, "v1.0.0", "unchanged"), repoReleases("c/d", 0, "", ""))
	msgs, err := Build(context.Background(), opt, digest.Digest{}, rel, false, true)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("want no releases message when no NewCount>0, got %d", len(msgs))
	}
}

func TestBuild_ReleasesFallbackListsChanged(t *testing.T) {
	opt := Options{Provider: nil, Lang: "zh", Cfg: baseConfig()}
	rel := releaseDigest(
		repoReleases("a/b", 2, "v1.2.0", "新增通知渠道"),
		repoReleases("c/d", 0, "v0.9.0", "无变更"),
		repoReleases("e/f", 1, "v2.0.0", "修复代理"),
	)
	msgs, _ := Build(context.Background(), opt, digest.Digest{}, rel, false, true)
	if len(msgs) != 1 {
		t.Fatalf("want 1 releases message, got %d", len(msgs))
	}
	body := msgs[0].Body
	if !strings.Contains(body, "a/b") || !strings.Contains(body, "e/f") {
		t.Errorf("body missing changed repos:\n%s", body)
	}
	if strings.Contains(body, "c/d") {
		t.Errorf("body lists unchanged repo c/d:\n%s", body)
	}
	if !strings.Contains(body, "2 new version") || !strings.Contains(body, "1 new version") {
		t.Errorf("body missing version counts:\n%s", body)
	}
}

func TestBuild_ModelPathWithFake(t *testing.T) {
	opt := Options{Provider: model.NewFake(), Lang: "zh", Cfg: baseConfig()}
	d := trendingDigest("daily", 2)
	msgs, err := Build(context.Background(), opt, d, releasedigest.ReleaseDigest{}, true, false)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].summarySource != SourceModel {
		t.Fatalf("summarySource = %q, want model (Fake returns text)", msgs[0].summarySource)
	}
	if msgs[0].usageCalls != 1 {
		t.Fatalf("usageCalls = %d, want 1", msgs[0].usageCalls)
	}
	if !strings.Contains(msgs[0].Body, "GitHub 趋势榜") {
		t.Errorf("body missing subject header:\n%s", msgs[0].Body)
	}
}

func TestBuild_FallbackOnProviderError(t *testing.T) {
	opt := Options{Provider: errProvider{}, Lang: "zh", Cfg: baseConfig()}
	d := trendingDigest("daily", 2)
	msgs, err := Build(context.Background(), opt, d, releasedigest.ReleaseDigest{}, true, false)
	if err != nil {
		t.Fatalf("Build must not fail on provider error: %v", err)
	}
	if msgs[0].summarySource != SourceFallback {
		t.Fatalf("summarySource = %q, want fallback on provider error", msgs[0].summarySource)
	}
	if msgs[0].usageCalls != 0 {
		t.Fatalf("usageCalls = %d, want 0 on fallback", msgs[0].usageCalls)
	}
}

func TestBuild_FallbackOnDegraded(t *testing.T) {
	opt := Options{Provider: degradedProvider{}, Lang: "zh", Cfg: baseConfig()}
	d := trendingDigest("daily", 2)
	msgs, _ := Build(context.Background(), opt, d, releasedigest.ReleaseDigest{}, true, false)
	if msgs[0].summarySource != SourceFallback {
		t.Fatalf("summarySource = %q, want fallback on degraded", msgs[0].summarySource)
	}
}

func TestBuild_EnglishLang(t *testing.T) {
	opt := Options{Provider: nil, Lang: "en", Cfg: baseConfig()}
	d := trendingDigest("weekly", 2)
	msgs, _ := Build(context.Background(), opt, d, releasedigest.ReleaseDigest{}, true, false)
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].Body, "GitHub Trending") {
		t.Errorf("english subject missing:\n%s", msgs[0].Body)
	}
	if !strings.Contains(msgs[0].Body, "more not shown") && !strings.Contains(msgs[0].Body, "repository") {
		// just ensure english phrasing path is exercised; content-specific.
		t.Logf("english body: %s", msgs[0].Body)
	}
}

func TestTruncateForChannel(t *testing.T) {
	lang := resolveLang("zh")
	big := strings.Repeat("字", 5000) // 15000 bytes, fits dingtalk but exceeds wecom(4000)
	// wecom limit → truncated with note.
	gotW := truncateForChannel(big, "wecom", 0, lang)
	if len(gotW) > limitWecom {
		t.Fatalf("wecom body %d bytes exceeds limit %d", len(gotW), limitWecom)
	}
	if !strings.Contains(gotW, "截断") {
		t.Errorf("wecom truncation missing note")
	}
	// dingtalk limit (18000) → not truncated.
	gotD := truncateForChannel(big, "dingtalk", 0, lang)
	if gotD != big {
		t.Errorf("dingtalk body should not be truncated (15000 < 18000)")
	}
	// maxBodyChars uniform hard cap tighter than channel default.
	gotCap := truncateForChannel(big, "dingtalk", 2000, lang)
	if len(gotCap) > 2000 {
		t.Fatalf("uniform cap body %d bytes exceeds 2000", len(gotCap))
	}
	// Valid UTF-8 preserved.
	if !utf8Valid(gotW) {
		t.Errorf("truncated wecom body is not valid utf-8")
	}
}

func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}

// ---- Deliver --------------------------------------------------------------

// fakeWebhook is a configurable httptest handler returning a body/status.
func fakeWebhook(t *testing.T, status int, body string, delay time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if delay > 0 {
			time.Sleep(delay)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func allEnabled(url string) config.NotifyConfig {
	cfg := baseConfig()
	cfg.Channels = config.NotifyChannels{
		DingTalk: config.NotifyChannel{Enabled: true, WebhookURL: url, MsgType: "markdown"},
		Lark:     config.NotifyChannel{Enabled: true, WebhookURL: url, MsgType: "text"},
		WeCom:    config.NotifyChannel{Enabled: true, WebhookURL: url, MsgType: "markdown"},
	}
	return cfg
}

func chanStatus(rep contract.NotifyReport, msgIdx int) map[string]string {
	out := map[string]string{}
	for _, c := range rep.Messages[msgIdx].Channels {
		out[c.Channel] = c.Status
	}
	return out
}

func TestDeliver_AllSent(t *testing.T) {
	srv := fakeWebhook(t, 200, `{"errcode":0,"errmsg":"ok","code":0,"msg":"success"}`, 0)
	opt := Options{Provider: nil, Lang: "zh", Cfg: allEnabled(srv.URL), HTTPClient: srv.Client()}
	msgs := []Message{{Kind: KindTrending, Period: "daily", Subject: "S", Body: "body", summarySource: SourceFallback}}

	rep := Deliver(context.Background(), opt, msgs)
	if !rep.Enabled || !rep.Attempted {
		t.Fatalf("report enabled/attempted = %v/%v", rep.Enabled, rep.Attempted)
	}
	if len(rep.Messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(rep.Messages))
	}
	st := chanStatus(rep, 0)
	for _, name := range []string{"dingtalk", "lark", "wecom"} {
		if st[name] != "sent" {
			t.Errorf("channel %s status = %q, want sent", name, st[name])
		}
	}
}

func TestDeliver_ErrcodeNonZeroFailed(t *testing.T) {
	// 200 HTTP but a non-zero business code → failed. DingTalk/WeCom report it in
	// "errcode"; Lark reports it in "code" (golib validates each provider's field).
	dingSrv := fakeWebhook(t, 200, `{"errcode":310000,"errmsg":"sign not match"}`, 0)
	larkSrv := fakeWebhook(t, 200, `{"code":9499,"msg":"bad request"}`, 0)
	wecomSrv := fakeWebhook(t, 200, `{"errcode":93000,"errmsg":"invalid webhook"}`, 0)

	cfg := baseConfig()
	cfg.Channels = config.NotifyChannels{
		DingTalk: config.NotifyChannel{Enabled: true, WebhookURL: dingSrv.URL, MsgType: "markdown"},
		Lark:     config.NotifyChannel{Enabled: true, WebhookURL: larkSrv.URL, MsgType: "text"},
		WeCom:    config.NotifyChannel{Enabled: true, WebhookURL: wecomSrv.URL, MsgType: "markdown"},
	}
	opt := Options{Provider: nil, Lang: "zh", Cfg: cfg, HTTPClient: dingSrv.Client()}
	msgs := []Message{{Kind: KindTrending, Period: "daily", Subject: "S", Body: "body"}}

	rep := Deliver(context.Background(), opt, msgs)
	st := chanStatus(rep, 0)
	for _, name := range []string{"dingtalk", "lark", "wecom"} {
		if st[name] != "failed" {
			t.Errorf("channel %s status = %q, want failed (business code!=0)", name, st[name])
		}
	}
	// failed must carry an error message.
	for _, c := range rep.Messages[0].Channels {
		if c.Status == "failed" && (c.Error == nil || *c.Error == "") {
			t.Errorf("channel %s failed but error is empty", c.Channel)
		}
	}
}

func TestDeliver_HTTP5xxFailed(t *testing.T) {
	srv := fakeWebhook(t, 502, `bad gateway`, 0)
	opt := Options{Provider: nil, Lang: "zh", Cfg: allEnabled(srv.URL), HTTPClient: srv.Client()}
	msgs := []Message{{Kind: KindTrending, Period: "daily", Subject: "S", Body: "body"}}

	rep := Deliver(context.Background(), opt, msgs)
	st := chanStatus(rep, 0)
	for _, name := range []string{"dingtalk", "lark", "wecom"} {
		if st[name] != "failed" {
			t.Errorf("channel %s status = %q, want failed (5xx)", name, st[name])
		}
	}
}

func TestDeliver_TimeoutFailed(t *testing.T) {
	// Server sleeps longer than the per-delivery timeout → transport error.
	srv := fakeWebhook(t, 200, `{"errcode":0}`, 300*time.Millisecond)
	cfg := allEnabled(srv.URL)
	cfg.TimeoutSeconds = 1 // gnotify timeout granularity is seconds; use a slow server + short client timeout below
	// Use an http.Client with a tiny timeout to force a deterministic timeout.
	client := &http.Client{Timeout: 50 * time.Millisecond}
	opt := Options{Provider: nil, Lang: "zh", Cfg: cfg, HTTPClient: client}
	msgs := []Message{{Kind: KindTrending, Period: "daily", Subject: "S", Body: "body"}}

	rep := Deliver(context.Background(), opt, msgs)
	st := chanStatus(rep, 0)
	for _, name := range []string{"dingtalk", "lark", "wecom"} {
		if st[name] != "failed" {
			t.Errorf("channel %s status = %q, want failed (timeout)", name, st[name])
		}
	}
}

func TestDeliver_DisabledChannel(t *testing.T) {
	srv := fakeWebhook(t, 200, `{"errcode":0}`, 0)
	cfg := baseConfig()
	cfg.Channels = config.NotifyChannels{
		DingTalk: config.NotifyChannel{Enabled: true, WebhookURL: srv.URL, MsgType: "markdown"},
		Lark:     config.NotifyChannel{Enabled: false},
		WeCom:    config.NotifyChannel{Enabled: false},
	}
	opt := Options{Provider: nil, Lang: "zh", Cfg: cfg, HTTPClient: srv.Client()}
	msgs := []Message{{Kind: KindTrending, Period: "daily", Subject: "S", Body: "body"}}

	rep := Deliver(context.Background(), opt, msgs)
	st := chanStatus(rep, 0)
	if st["dingtalk"] != "sent" {
		t.Errorf("dingtalk = %q, want sent", st["dingtalk"])
	}
	if st["lark"] != "disabled" || st["wecom"] != "disabled" {
		t.Errorf("disabled channels = %q/%q, want disabled", st["lark"], st["wecom"])
	}
}

func TestDeliver_NoMessages(t *testing.T) {
	opt := Options{Lang: "zh", Cfg: baseConfig()}
	rep := Deliver(context.Background(), opt, nil)
	if rep.Attempted {
		t.Errorf("attempted = true with no messages, want false")
	}
	if !rep.Enabled {
		t.Errorf("enabled = false, want true")
	}
}

func TestDeliver_UsageRecorded(t *testing.T) {
	srv := fakeWebhook(t, 200, `{"errcode":0}`, 0)
	opt := Options{Provider: nil, Lang: "zh", Cfg: allEnabled(srv.URL), HTTPClient: srv.Client()}
	msgs := []Message{{
		Kind: KindTrending, Period: "daily", Subject: "S", Body: "body", summarySource: SourceModel,
		usage: model.Usage{TotalTokens: 830, Known: true}, usageCalls: 1,
	}}
	rep := Deliver(context.Background(), opt, msgs)
	if rep.Usage == nil {
		t.Fatalf("usage missing")
	}
	if got, _ := rep.Usage["totalTokens"].(int); got != 830 {
		t.Errorf("totalTokens = %v, want 830", rep.Usage["totalTokens"])
	}
	if known, _ := rep.Usage["usageKnown"].(bool); !known {
		t.Errorf("usageKnown = %v, want true", rep.Usage["usageKnown"])
	}
	// JSON-serialisable (contract wire).
	if _, err := json.Marshal(rep); err != nil {
		t.Fatalf("report not JSON-serialisable: %v", err)
	}
}

// recordingWebhook captures the byte length of each received body so we can
// assert per-channel truncation actually differed across channels.
func recordingWebhook(t *testing.T, lens *[]int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Text *struct {
				Content string `json:"content"`
			} `json:"text"`
			Markdown *struct {
				Text string `json:"text"`
			} `json:"markdown"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		n := 0
		if payload.Markdown != nil {
			n = len(payload.Markdown.Text)
		} else if payload.Text != nil {
			n = len(payload.Text.Content)
		}
		*lens = append(*lens, n)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"errcode":0,"code":0}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDeliver_PerChannelTruncation(t *testing.T) {
	// Body of 6000 runes (18000 bytes): fits dingtalk(18000)/lark(28000) but the
	// wecom 4000-byte ceiling forces truncation only for wecom.
	var wecomLens, dingLens, larkLens []int
	wecomSrv := recordingWebhook(t, &wecomLens)
	dingSrv := recordingWebhook(t, &dingLens)
	larkSrv := recordingWebhook(t, &larkLens)

	cfg := baseConfig()
	cfg.Channels = config.NotifyChannels{
		DingTalk: config.NotifyChannel{Enabled: true, WebhookURL: dingSrv.URL, MsgType: "markdown"},
		Lark:     config.NotifyChannel{Enabled: true, WebhookURL: larkSrv.URL, MsgType: "text"},
		WeCom:    config.NotifyChannel{Enabled: true, WebhookURL: wecomSrv.URL, MsgType: "markdown"},
	}
	opt := Options{Provider: nil, Lang: "zh", Cfg: cfg, HTTPClient: wecomSrv.Client()}
	body := strings.Repeat("字", 6000)
	msgs := []Message{{Kind: KindTrending, Period: "daily", Subject: "S", Body: body}}

	rep := Deliver(context.Background(), opt, msgs)
	if len(rep.Messages[0].Channels) != 3 {
		t.Fatalf("got %d channel results", len(rep.Messages[0].Channels))
	}
	if len(wecomLens) != 1 || len(dingLens) != 1 || len(larkLens) != 1 {
		t.Fatalf("webhook hits wecom/ding/lark = %d/%d/%d, want 1/1/1", len(wecomLens), len(dingLens), len(larkLens))
	}
	if wecomLens[0] > limitWecom {
		t.Errorf("wecom received %d bytes, want <= %d (truncated)", wecomLens[0], limitWecom)
	}
	if dingLens[0] <= limitWecom {
		t.Errorf("dingtalk received %d bytes, expected full (untruncated) body", dingLens[0])
	}
}
