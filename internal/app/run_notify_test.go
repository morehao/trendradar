package app

// Offline tests for the notify epilogue wired into Run (WS-D). They drive a
// full run against a fake git remote (setupRepo) plus a fake model provider and
// a local httptest webhook standing in for DingTalk / Lark / WeCom, so the whole
// path (finishNotify -> notify.Build -> notify.Deliver) is exercised without any
// network. Mirrors run_releases_test.go / run_test.go for the fake-core+provider
// setup.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	contract "github.com/morehao/trendradar/contract"
	"github.com/morehao/trendradar/internal/config"
	"github.com/morehao/trendradar/internal/fetch"
	"github.com/morehao/trendradar/internal/model"
	"github.com/morehao/trendradar/internal/releasewatch"
)

// notifyWebhook is a fake chat webhook that echoes a successful errcode.
func notifyWebhook(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// notifyOnConfig builds an enabled notify config with all three channels
// pointing at one webhook URL.
func notifyOnConfig(url string) config.NotifyConfig {
	on := true
	return config.NotifyConfig{
		Enabled:            &on,
		Sections:           config.NotifySections{},
		Strategy:           "broadcast",
		MaxReposPerSection: 10,
		MaxBodyChars:       4000,
		SummaryMaxChars:    800,
		TimeoutSeconds:     10,
		Channels: config.NotifyChannels{
			DingTalk: config.NotifyChannel{Enabled: true, WebhookURL: url, MsgType: "markdown"},
			Lark:     config.NotifyChannel{Enabled: true, WebhookURL: url, MsgType: "text"},
			WeCom:    config.NotifyChannel{Enabled: true, WebhookURL: url, MsgType: "markdown"},
		},
	}
}

// notifyKinds returns the message kinds present in a NotifyReport.
func notifyKinds(rep *contract.NotifyReport) []string {
	if rep == nil {
		return nil
	}
	var out []string
	for _, m := range rep.Messages {
		out = append(out, m.Kind)
	}
	return out
}

// releasesOnlyOpts builds Options for a release-only run over one repo with one
// fresh release, plus the injected webhook client.
func releasesOnlyOpts(work, url string, client *http.Client) (Options, *fakeReleaseFetcher) {
	svc := &fakeReleaseFetcher{releases: map[string][]fetch.Release{
		"o/r": {{ID: 1, TagName: "v1", Name: "v1", Body: "first", HTMLURL: "https://github.com/o/r/releases/tag/v1"}},
	}}
	tm := time.Unix(1700000000, 0).UTC()
	svc.releases["o/r"][0].PublishedAt = &tm
	return Options{
		Provider:         model.NewFake(),
		Stages:           []string{StageReleases},
		NotifyHTTPClient: client,
		Releases: &ReleasesOptions{
			Repos:     []string{"o/r"},
			Fetcher:   svc,
			StatePath: releasewatch.StatePath(work),
			CachePath: filepath.Join(work, ".git", "trendradar-release-cache.json"),
		},
	}, svc
}

// Both sections fire for a trending+releases run whose digest and release stage
// both produced content.
func TestNotify_BothSectionsSent(t *testing.T) {
	_, work, cfg := setupRepo(t)
	cfg.Releases = weeklyReleasesConfig(work)
	srv := notifyWebhook(t, `{"errcode":0,"errmsg":"ok","code":0,"msg":"success"}`)
	cfg.Notify = notifyOnConfig(srv.URL)

	ropts, _ := releasesOnlyOpts(work, srv.URL, srv.Client())
	opts := ropts
	opts.Stages = nil // all enabled: trending + releases
	res, err := Run(context.Background(), cfg, fixtureInputs(), opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Notify == nil {
		t.Fatal("res.Notify is nil, want a report")
	}
	if !res.Notify.Enabled || !res.Notify.Attempted {
		t.Fatalf("notify enabled/attempted = %v/%v", res.Notify.Enabled, res.Notify.Attempted)
	}
	kinds := notifyKinds((res.Notify))
	if len(kinds) != 2 || kinds[0] != "trending" || kinds[1] != "releases" {
		t.Fatalf("message kinds = %v, want [trending releases]", kinds)
	}
	for _, m := range res.Notify.Messages {
		for _, c := range m.Channels {
			if c.Status != "sent" {
				t.Errorf("%s/%s status = %q, want sent", m.Kind, c.Channel, c.Status)
			}
		}
	}
}

// Only the trending section fires when the releases section is turned off.
func TestNotify_TrendingOnly(t *testing.T) {
	_, work, cfg := setupRepo(t)
	cfg.Releases = weeklyReleasesConfig(work)
	srv := notifyWebhook(t, `{"errcode":0}`)
	ncfg := notifyOnConfig(srv.URL)
	off := false
	ncfg.Sections.Trending = nil
	ncfg.Sections.Releases = &off
	cfg.Notify = ncfg

	ropts, _ := releasesOnlyOpts(work, srv.URL, srv.Client())
	opts := ropts
	opts.Stages = nil
	res, err := Run(context.Background(), cfg, fixtureInputs(), opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Notify == nil {
		t.Fatal("res.Notify is nil")
	}
	kinds := notifyKinds((res.Notify))
	if len(kinds) != 1 || kinds[0] != "trending" {
		t.Fatalf("message kinds = %v, want [trending]", kinds)
	}
}

// A release-only run has no trending digest, so only the releases section is
// pushed even though both sections are enabled.
func TestNotify_ReleaseOnlyNoTrending(t *testing.T) {
	_, work, cfg := setupRepo(t)
	cfg.Releases = weeklyReleasesConfig(work)
	srv := notifyWebhook(t, `{"errcode":0}`)
	cfg.Notify = notifyOnConfig(srv.URL)

	opts, _ := releasesOnlyOpts(work, srv.URL, srv.Client())
	res, err := Run(context.Background(), cfg, Inputs{Date: "2026-10-30"}, opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Digest.Date != "" {
		t.Fatalf("release-only run has a trending digest date %q", res.Digest.Date)
	}
	if res.Notify == nil {
		t.Fatal("res.Notify is nil")
	}
	kinds := notifyKinds((res.Notify))
	if len(kinds) != 1 || kinds[0] != "releases" {
		t.Fatalf("message kinds = %v, want [releases]", kinds)
	}
}

// A failing channel is recorded on the report, but Run still returns nil error.
func TestNotify_ChannelFailedRunStillOK(t *testing.T) {
	_, work, cfg := setupRepo(t)
	cfg.Releases = weeklyReleasesConfig(work)
	// errcode != 0 => business failure (golib marks the channel failed).
	srv := notifyWebhook(t, `{"errcode":310000,"errmsg":"sign not match"}`)
	cfg.Notify = notifyOnConfig(srv.URL)

	opts, _ := releasesOnlyOpts(work, srv.URL, srv.Client())
	res, err := Run(context.Background(), cfg, Inputs{Date: "2026-10-30"}, opts)
	if err != nil {
		t.Fatalf("run returned error %v, a channel failure must not fail the run", err)
	}
	if res.Notify == nil || !res.Notify.Attempted {
		t.Fatalf("res.Notify = %+v, want attempted report", res.Notify)
	}
	sawFailed := false
	for _, m := range res.Notify.Messages {
		for _, c := range m.Channels {
			if c.Status == "failed" {
				sawFailed = true
			}
		}
	}
	if !sawFailed {
		t.Fatal("expected at least one failed channel in the report")
	}
}

// dry-run must not invoke the notify stage at all.
func TestNotify_DryRunDoesNotSend(t *testing.T) {
	_, work, cfg := setupRepo(t)
	cfg.Releases = weeklyReleasesConfig(work)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"errcode":0}`))
	}))
	t.Cleanup(srv.Close)
	cfg.Notify = notifyOnConfig(srv.URL)

	opts, _ := releasesOnlyOpts(work, srv.URL, srv.Client())
	opts.DryRun = true
	res, err := Run(context.Background(), cfg, Inputs{Date: "2026-10-30"}, opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Notify != nil {
		t.Fatalf("dry-run produced a notify report: %+v", res.Notify)
	}
	if hits != 0 {
		t.Fatalf("dry-run hit the webhook %d times, want 0", hits)
	}
}
