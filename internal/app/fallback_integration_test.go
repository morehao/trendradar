package app

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/morehao/golib/gllm"
	"github.com/morehao/trendradar/internal/config"
	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/model"
)

// This file wires the model fallback chain (internal/model.FallbackProvider,
// A2) into the real app.Run pipeline (A3 packs it as Options.Provider) and
// asserts the run-level degradation semantics end to end, offline.
//
// We deliberately drive the *real* Run in DryRun mode: it exercises the exact
// summarize -> repoDigests -> SummarySource -> SummaryDegraded -> warning path
// that production uses (run.go around line 355-370) without needing git, a
// remote, or any network. The fake provider chain replaces the real model.

// chainProvider is an offline member of a fallback chain. When fail is set its
// Complete returns that error; otherwise it behaves like model.Fake (emits a
// valid Chinese-JSON summary for every REPO= line in the prompt). This lets a
// test simulate "primary 401, backup fine" without touching the network.
type chainProvider struct {
	name string
	fail error
}

func (c *chainProvider) Name() string { return c.name }

var integrationRepoLineRe = regexp.MustCompile(`(?m)^REPO=(\S+)\tDESC=(.*)$`)

func (c *chainProvider) Complete(_ context.Context, _, user string) (model.Result, error) {
	if c.fail != nil {
		return model.Result{}, c.fail
	}
	type item struct {
		FullName string `json:"fullName"`
		Summary  string `json:"summary"`
	}
	var items []item
	for _, m := range integrationRepoLineRe.FindAllStringSubmatch(user, -1) {
		items = append(items, item{FullName: m[1], Summary: "[chain] 这是离线集成测试生成的中文示例摘要，仅用于验证 fallback 流程。"})
	}
	b, err := json.Marshal(map[string]any{"summaries": items})
	if err != nil {
		return model.Result{}, err
	}
	return model.Result{Text: string(b)}, nil
}

// fallbackInputs builds a tiny two-repo snapshot set for a dry-run digest.
func fallbackInputs() Inputs {
	mk := func(i int, name string) digest.RepoSnapshot {
		return digest.RepoSnapshot{
			FullName: name, URL: "https://github.com/" + name,
			Description: digest.StrPtr(name + " description"),
			Language:    "go", Stars: 100 + i, StarsInPeriod: 10,
			Forks: digest.IntPtr(5), Rank: i + 1, Source: digest.SourceTrendingHTML,
		}
	}
	return Inputs{
		Repos:  []digest.RepoSnapshot{mk(0, "acme/one"), mk(1, "acme/two")},
		Date:   "2026-11-14",
		Period: "daily",
	}
}

// dryRunCfg is the minimal config a DryRun needs; Output.Dir keeps the render
// root self-contained and no git remote is touched on the dry-run path.
func dryRunCfg() *config.Config {
	return &config.Config{
		Output:   config.OutputConfig{Dir: "digests"},
		Trending: config.TrendingConfig{Since: "daily"},
	}
}

// collectSources returns the SummarySource of each repo digest in order.
func collectSources(d digest.Digest) []string {
	out := make([]string, 0, len(d.Repos))
	for _, rd := range d.Repos {
		out = append(out, rd.SummarySource)
	}
	return out
}

// hasWarning reports whether any warning contains sub.
func hasWarning(warnings []string, sub string) bool {
	for _, w := range warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

// Scenario 1: primary returns an auth (401) error (fallback-worthy), backup
// returns a valid JSON summary -> the chain succeeds, every repo is sourced
// from "model", no degradation warning is emitted and SummaryDegraded is false.
func TestFallbackIntegration_PrimaryAuthFails_BackupSucceeds(t *testing.T) {
	primary := &chainProvider{name: "primary", fail: gllm.ErrAuth.Wrap(errors.New("401 unauthorized"))}
	backup := &chainProvider{name: "backup"}
	provider := model.NewFallback(primary, backup)

	cfg := dryRunCfg()
	res, err := Run(context.Background(), cfg, fallbackInputs(), Options{
		DryRun:   true,
		Provider: provider,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Report.SummaryDegraded {
		t.Fatalf("SummaryDegraded = true, want false (backup succeeded)")
	}
	for i, src := range collectSources(res.Digest) {
		if src != "model" {
			t.Fatalf("repo[%d] SummarySource = %q, want model", i, src)
		}
	}
	if hasWarning(res.Report.Warnings, "降级为英文原文") {
		t.Fatalf("unexpected degradation warning: %v", res.Report.Warnings)
	}
	// The chain records which member served the run.
	if got := provider.Used(); got != "backup" {
		t.Fatalf("Used() = %q, want backup", got)
	}
}

// Scenario 2: the whole chain fails (auth on primary AND rate limit on the
// backup: rate limit is NOT fallback-worthy, but here the primary's auth error
// already exhausted the chain — either way every member fails) -> every repo
// degrades to the original description, the "降级为英文原文" warning appears and
// SummaryDegraded is true.
//
// To force a true all-fail we make both members return fallback-worthy auth
// errors, so each is tried and the chain returns its last error.
func TestFallbackIntegration_AllFail_DegradesToOriginal(t *testing.T) {
	primary := &chainProvider{name: "primary", fail: gllm.ErrAuth.Wrap(errors.New("401 primary"))}
	backup := &chainProvider{name: "backup", fail: gllm.ErrAuth.Wrap(errors.New("401 backup"))}
	provider := model.NewFallback(primary, backup)

	cfg := dryRunCfg()
	in := fallbackInputs()
	res, err := Run(context.Background(), cfg, in, Options{
		DryRun:   true,
		Provider: provider,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Report.SummaryDegraded {
		t.Fatalf("SummaryDegraded = false, want true (all members failed)")
	}
	for i, src := range collectSources(res.Digest) {
		if src != "original" {
			t.Fatalf("repo[%d] SummarySource = %q, want original", i, src)
		}
	}
	wantWarn := "2/2 仓库摘要降级为英文原文"
	if !hasWarning(res.Report.Warnings, wantWarn) {
		t.Fatalf("missing degradation warning %q in %v", wantWarn, res.Report.Warnings)
	}
	// No member succeeded, so Used() stays empty.
	if got := provider.Used(); got != "" {
		t.Fatalf("Used() = %q, want empty", got)
	}
	// The digest is flagged degraded at the document level too.
	if !res.Digest.Degraded {
		t.Fatalf("digest.Degraded = false, want true")
	}
}

// Scenario 3: AllowDegraded=false with the whole chain failing keeps the
// existing "run does not error, but the period's summaries are degraded"
// semantics. AllowDegraded is a *provider* (gllm) knob, not an app.Run input:
// at the app layer the observable contract is identical to scenario 2 — the run
// still completes (no error), the digest is rendered, and the degradation is
// surfaced via SummarySource/SummaryDegraded/warning rather than an error.
//
// We assert that "all fail + AllowDegraded=false" does NOT turn the run into a
// hard error: Run returns a result, not an error, and the degradation is fully
// observable. This pins the current behaviour so a future change that starts
// failing the run here is caught. (AllowDegraded=false is the default zero value.)
func TestFallbackIntegration_AllowDegradedFalse_AllFail_RunStillCompletes(t *testing.T) {
	primary := &chainProvider{name: "primary", fail: gllm.ErrAuth.Wrap(errors.New("401 primary"))}
	backup := &chainProvider{name: "backup", fail: gllm.ErrTimeout.Wrap(errors.New("timeout"))}
	provider := model.NewFallback(primary, backup)

	cfg := dryRunCfg()
	cfg.Model.AllowDegraded = false // default, made explicit for the scenario
	in := fallbackInputs()

	res, err := Run(context.Background(), cfg, in, Options{
		DryRun:   true,
		Provider: provider,
	})
	if err != nil {
		t.Fatalf("Run returned a hard error, want a completed degraded run: %v", err)
	}
	// Summaries fall back to the original description; the run itself succeeds.
	for i, src := range collectSources(res.Digest) {
		if src != "original" {
			t.Fatalf("repo[%d] SummarySource = %q, want original", i, src)
		}
	}
	if !res.Report.SummaryDegraded {
		t.Fatalf("SummaryDegraded = false, want true")
	}
	if !hasWarning(res.Report.Warnings, "降级为英文原文") {
		t.Fatalf("missing degradation warning in %v", res.Report.Warnings)
	}
	// A degraded-but-rendered digest still carries the repos (period not empty).
	if res.Digest.Count != len(in.Repos) {
		t.Fatalf("digest.Count = %d, want %d", res.Digest.Count, len(in.Repos))
	}
}

// C1: the model that actually served the run is reported back on
// Report.Usage.usedModel so the plugin's run history can show it.
//
// Scenario: primary fails with an auth error (fallback-worthy), the backup
// serves every repo -> Usage.UsedModel must be the backup member's Name()
// ("backup"), proving the chain's Used() is threaded through to the contract.
func TestUsage_ReportsUsedModel_BackupServed(t *testing.T) {
	primary := &chainProvider{name: "primary", fail: gllm.ErrAuth.Wrap(errors.New("401 unauthorized"))}
	backup := &chainProvider{name: "backup-name"}
	provider := model.NewFallback(primary, backup)

	res, err := Run(context.Background(), dryRunCfg(), fallbackInputs(), Options{
		DryRun:   true,
		Provider: provider,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Report.Usage == nil {
		t.Fatalf("Report.Usage = nil, want a usage block")
	}
	if res.Report.Usage.UsedModel == nil {
		t.Fatalf("Usage.UsedModel = nil, want %q", "backup-name")
	}
	if got := *res.Report.Usage.UsedModel; got != "backup-name" {
		t.Fatalf("Usage.UsedModel = %q, want %q", got, "backup-name")
	}
}

// When the primary serves the whole run, usedModel is the primary member.
func TestUsage_ReportsUsedModel_PrimaryServed(t *testing.T) {
	primary := &chainProvider{name: "primary-name"}
	backup := &chainProvider{name: "backup-name"}
	provider := model.NewFallback(primary, backup)

	res, err := Run(context.Background(), dryRunCfg(), fallbackInputs(), Options{
		DryRun:   true,
		Provider: provider,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Report.Usage == nil || res.Report.Usage.UsedModel == nil {
		t.Fatalf("Usage.UsedModel = nil, want %q", "primary-name")
	}
	if got := *res.Report.Usage.UsedModel; got != "primary-name" {
		t.Fatalf("Usage.UsedModel = %q, want %q", got, "primary-name")
	}
}

// When the whole chain fails (degrade to original), no member served the run so
// usedModel stays empty (nil pointer -> omitted from JSON). This pins the "best
// effort / empty when unknown" contract.
func TestUsage_ReportsUsedModel_EmptyWhenAllFail(t *testing.T) {
	primary := &chainProvider{name: "primary", fail: gllm.ErrAuth.Wrap(errors.New("401 primary"))}
	backup := &chainProvider{name: "backup", fail: gllm.ErrAuth.Wrap(errors.New("401 backup"))}
	provider := model.NewFallback(primary, backup)

	res, err := Run(context.Background(), dryRunCfg(), fallbackInputs(), Options{
		DryRun:   true,
		Provider: provider,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Report.Usage == nil {
		t.Fatalf("Report.Usage = nil, want a usage block")
	}
	if res.Report.Usage.UsedModel != nil {
		t.Fatalf("Usage.UsedModel = %q, want nil (no member served)", *res.Report.Usage.UsedModel)
	}
}

// A non-fallback (single-model) provider has no Used(); usedModel is the
// configured primary model name (cfg.Model.Name). The Fake provider still emits
// valid summaries, so the chain-free path serves the run.
func TestUsage_ReportsUsedModel_SingleModelUsesCfgName(t *testing.T) {
	cfg := dryRunCfg()
	cfg.Model.Name = "gpt-4o-mini"

	res, err := Run(context.Background(), cfg, fallbackInputs(), Options{
		DryRun:   true,
		Provider: model.NewFake(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Report.Usage == nil || res.Report.Usage.UsedModel == nil {
		t.Fatalf("Usage.UsedModel = nil, want %q", "gpt-4o-mini")
	}
	if got := *res.Report.Usage.UsedModel; got != "gpt-4o-mini" {
		t.Fatalf("Usage.UsedModel = %q, want %q", got, "gpt-4o-mini")
	}
}
