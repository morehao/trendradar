// Package app orchestrates a full run: summarize, render, commit, push and
// (optionally) open a pull request.
package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	contract "github.com/morehao/trendradar/contract"
	"github.com/morehao/trendradar/internal/config"
	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/enrich"
	"github.com/morehao/trendradar/internal/gitx"
	"github.com/morehao/trendradar/internal/ingest"
	"github.com/morehao/trendradar/internal/ipc"
	"github.com/morehao/trendradar/internal/model"
	"github.com/morehao/trendradar/internal/pr"
	"github.com/morehao/trendradar/internal/releasedigest"
	"github.com/morehao/trendradar/internal/releasedoc"
	"github.com/morehao/trendradar/internal/releasewatch"
	"github.com/morehao/trendradar/internal/render"
	"github.com/morehao/trendradar/internal/subscription"
	"github.com/morehao/trendradar/internal/summarize"
)

// Inputs are the snapshots for a run (already fetched/parsed).
type Inputs struct {
	Repos          []digest.RepoSnapshot
	Degraded       bool
	DegradedReason string
	DegradedLangs  []string
	// DegradedDetail is the compact "lang=reason" list behind DegradedLangs
	// ("python=timeout, typescript=status 429") or a one-line explanation when
	// the degradation was not per-language. Empty for a clean run. It is
	// appended to the degradation warnings so a run record says *why*.
	DegradedDetail string
	Date           string
	Period         string
	// Requested is the language list the caller asked to fetch (cfg.trending.
	// languages, in order). When non-empty it is echoed verbatim into the
	// digest's `languages` front matter so the document matches what the user
	// selected instead of being reverse-derived from whatever repos were seen.
	// Empty on the --input replay path, which falls back to languagesOf(Repos).
	Requested []string
	// ByPage is the per-page view (page language -> repos) used to section the
	// markdown: one "## <lang>" block per requested language, with "all" kept as
	// a single block. Nil on the --input replay path or search fallback, where
	// the renderer falls back to the legacy per-repo-language layout.
	ByPage map[string][]digest.RepoSnapshot
}

// Trigger says who asked for the run. It only changes the PR lifecycle policy:
// an automated run respects a human closing the PR, while a manual run is an
// explicit request to re-open it.
type Trigger string

const (
	TriggerManual   Trigger = "manual"
	TriggerSchedule Trigger = "schedule"
)

// Stage names accepted by RunRequest.stages.
const (
	StageTrending = "trending"
	StageDigest   = "digest"
	StageReleases = "releases"
)

// Options controls a run.
type Options struct {
	DryRun   bool
	Token    string           // optional GitHub token for push/PR
	Provider model.Provider   // optional; defaults to the offline Fake
	Trigger  Trigger          // optional; defaults to TriggerManual
	Enricher *enrich.Enricher // optional; nil means no external context
	Releases *ReleasesOptions // optional; nil means no release subscription stage
	// Stages narrows the run (RunRequest.stages). Empty means every enabled
	// stage, which is the historical behaviour. A release-only run passes
	// [releases]: no trending fetch, no digest render.
	Stages []string
	// Force ignores the persisted summary cache so the model is re-asked and the
	// digest is regenerated. It does NOT change the append-only commit logic: if
	// the freshly rendered artifacts are byte-identical to the branch, the run
	// still skips with skipReason=unchanged and creates no commit.
	Force bool
}

// stageEnabled reports whether name should run for this Options.
func (o Options) stageEnabled(name string) bool {
	if len(o.Stages) == 0 {
		return true
	}
	for _, s := range o.Stages {
		if s == name {
			return true
		}
	}
	return false
}

// ValidStages validates a stages list and returns it unchanged. An empty list is
// valid and means "all".
func ValidStages(stages []string) error {
	seen := map[string]bool{}
	for _, s := range stages {
		switch s {
		case StageTrending, StageDigest, StageReleases:
		default:
			return fmt.Errorf("unknown stage %q (want trending|digest|releases)", s)
		}
		if seen[s] {
			return fmt.Errorf("duplicate stage %q", s)
		}
		seen[s] = true
	}
	return nil
}

// ReleasesOptions carries the already-resolved subscription data for the
// optional releases stage (cmd resolves the subscription file and constructs
// the fetcher; app only orchestrates).
type ReleasesOptions struct {
	Repos     []string
	Fetcher   releasewatch.Fetcher
	StatePath string
	CachePath string

	// Repo-versioned subscriptions (design 2026-10-31). When SubscriptionsPath is
	// non-empty, run lands the merged list on the checked-out date branch and
	// clears the pending queue only after a successful push. Repos above is the
	// poll list (branch ∪ pending), computed by cmd; it must equal what run would
	// derive here.
	SubscriptionsPath string
	// PendingRepos is the .git pending queue. HasPending distinguishes "no queue"
	// from "an empty queue that removes everything".
	PendingRepos []string
	HasPending   bool
}

// Result is the outcome of a run.
type Result struct {
	Digest digest.Digest
	// DigestPath is the repo-relative path of the markdown digest rendered by
	// this run (empty when no digest was rendered: dry-run/merged/pr-closed).
	DigestPath string
	Files      []render.File
	Report     Report
	Skipped    bool
	SkipReason string // merged | pr-closed | unchanged
	Branch     string
	Commit     string
	PRURL      string
}

// Run executes the pipeline.
func Run(ctx context.Context, cfg *config.Config, in Inputs, opts Options) (Result, error) {
	start := time.Now()
	var res Result
	rep := Report{}

	provider := opts.Provider
	if provider == nil {
		provider = model.NewFake()
	}

	genAt := time.Now().UTC()

	// Stage selection. An empty Stages list means "every enabled stage", which
	// is what every caller before RunRequest.stages did. A release-only run
	// (stages: ["releases"]) must not fetch trending, call the model or render a
	// digest: it writes only release artifacts, so its commit cannot drift with
	// the day's trending summary.
	// trending and digest are one pipeline (digest cannot exist without the
	// trending snapshots), so either name enables it.
	wantsDigest := opts.stageEnabled(StageTrending) || opts.stageEnabled(StageDigest)

	// Output root and branch components are needed by both stage sets, so they
	// are resolved before the digest branch. output.dir stays the single source
	// of truth for rendering and `git add` (dry runs have no cfg).
	outDir := render.OutputRoot("")
	if cfg != nil {
		outDir = render.OutputRoot(cfg.Output.Dir)
	}
	// The period labels both the digest and the branch. A digest run uses the
	// trending period; a release-only run uses the releases summary period so
	// subscribe/serve resolve the same branch name (RunBranchName).
	period := render.PeriodOf(digest.Digest{Period: in.Period})
	if !wantsDigest && cfg != nil {
		period = render.PeriodOf(digest.Digest{Period: cfg.Releases.Period})
	}
	name := render.NameOf(period, in.Date)
	historyRoot := outDir
	if cfg != nil && cfg.Repo.Path != "" {
		historyRoot = filepath.Join(cfg.Repo.Path, outDir)
	}
	limits := map[string]int{"daily": 90, "weekly": 12, "monthly": 12}
	var files []render.File
	var d digest.Digest

	if wantsDigest {
		sumStart := time.Now()
		// Persisted summary cache: without it every run re-asks the model, the
		// rendered text drifts, and "same input" never yields the same digest.
		// The cache is keyed by repo+description+prompt/model namespace, so a model
		// or prompt change misses instead of silently reusing stale prose.
		cachePath := ""
		if cfg != nil && !opts.DryRun && cfg.Repo.Path != "" {
			cachePath = summarize.CachePath(cfg.Repo.Path)
		}
		// --force: start from an empty in-memory cache so every lookup misses and
		// the model is re-asked. We still Save() below, so the new prose warms the
		// on-disk cache for the next non-forced run. Nothing about the append-only
		// commit decision changes — an identical re-render still skips.
		var summaryCache *summarize.Cache
		if opts.Force {
			summaryCache = summarize.NewCache()
		} else {
			summaryCache, _ = summarize.LoadCache(cachePath)
			if summaryCache == nil {
				summaryCache = summarize.NewCache()
			}
		}
		enrichMode := enrich.ModeNone
		if opts.Enricher != nil {
			enrichMode = opts.Enricher.Mode()
		}
		if cfg != nil {
			summaryCache.SetNamespace(summarize.NamespaceForMode(cfg.Model.Provider, cfg.Model.Name, string(enrichMode)))
		}
		batchSize, maxRetries := 10, 1
		if cfg != nil {
			if cfg.Summarize.BatchSize > 0 {
				batchSize = cfg.Summarize.BatchSize
			}
			if cfg.Summarize.MaxRetries > 0 {
				maxRetries = cfg.Summarize.MaxRetries
			}
		}
		summarizer := summarize.New(provider,
			summarize.WithCache(summaryCache),
			summarize.WithBatchSize(batchSize),
			summarize.WithMaxRetries(maxRetries),
		)

		// Optional external context. Only repos whose summary is not cached are
		// enriched, so a warm run spends no extra GitHub requests. All failures are
		// best-effort: they surface as warnings and never fail the run.
		var byRepo map[string]summarize.Context
		if opts.Enricher != nil && enrichMode != enrich.ModeNone {
			enrichStart := time.Now()
			pending := summarizer.Pending(in.Repos)
			ctxs, er := opts.Enricher.Enrich(ctx, pending)
			if len(ctxs) > 0 {
				byRepo = make(map[string]summarize.Context, len(ctxs))
				for name, c := range ctxs {
					byRepo[name] = summarize.Context{Topics: c.Topics, ReadmeExcerpt: c.ReadmeExcerpt}
				}
			}
			if err := opts.Enricher.SaveCache(); err != nil {
				rep.Warnings = append(rep.Warnings, "enrich-cache: "+err.Error())
			}
			rep.Enrich = &EnrichReport{
				Mode:        er.Mode,
				Requested:   er.Requested,
				Hit:         er.Hit,
				Fetched:     er.Fetched,
				Failed:      er.Failed,
				RateLimited: er.RateLimited,
				Skipped:     er.Skipped,
			}
			rep.Stages = append(rep.Stages, Stage{Name: "enrich", DurationMs: since(enrichStart)})
			if er.Failed > 0 || er.RateLimited > 0 {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf(
					"enrich requested=%d hit=%d fetched=%d failed=%d rateLimited=%d", er.Requested, er.Hit, er.Fetched, er.Failed, er.RateLimited))
			}
		}

		repoDigests := summarizer.SummarizeWithContext(ctx, in.Repos, byRepo)
		if cachePath != "" {
			if err := summaryCache.Save(cachePath); err != nil {
				rep.Warnings = append(rep.Warnings, "cache: "+err.Error())
			}
		}
		// Snapshot token spend before the pipeline can early-return (skipped/merged),
		// so every run that reached summarization reports its usage.
		{
			st := summarizer.Stats()
			rep.Usage = &contract.Usage{
				PromptTokens:     st.Usage.PromptTokens,
				CompletionTokens: st.Usage.CompletionTokens,
				TotalTokens:      st.Usage.TotalTokens,
				CachedTokens:     st.Usage.CachedTokens,
				UsageKnown:       st.Usage.Known,
				Calls:            st.Calls,
				Retries:          st.Retries,
				Batches:          st.Batches,
				CacheHits:        st.CacheHits,
			}
		}
		degradedCount := 0
		for _, rd := range repoDigests {
			if rd.SummarySource == "original" {
				degradedCount++
			}
		}
		summaryDegraded := degradedCount > 0
		rep.SummaryDegraded = summaryDegraded
		rep.Stages = append(rep.Stages, Stage{Name: "summarize", DurationMs: since(sumStart), Degraded: summaryDegraded})

		// Assemble degradation banners/warnings. These explain *why* the digest is
		// degraded so it is never shown as if it were a clean trending result.
		var banners []string
		if summaryDegraded {
			banners = append(banners, "> ⚠️ 本次摘要有降级：模型不可用或返回了无效结果，部分条目为英文原文（summarySource=original）。\n")
			rep.Warnings = append(rep.Warnings, fmt.Sprintf(
				"%d/%d 仓库摘要降级为英文原文（模型不可用或返回无效结果）", degradedCount, len(repoDigests)))
		}
		switch in.DegradedReason {
		case ingest.ReasonSearchFallback, ingest.ReasonHTMLPartial:
			if banner, warning := ingestDegradation(in.DegradedReason, in.DegradedLangs, in.DegradedDetail); warning != "" {
				banners = append(banners, banner)
				rep.Warnings = append(rep.Warnings, warning)
			}
		}

		coreV := ipc.Version
		genAt := time.Now().UTC()
		d = digest.Digest{
			Schema:          2,
			Date:            in.Date,
			Period:          period,
			Languages:       digestLanguages(in),
			Count:           len(repoDigests),
			Degraded:        in.Degraded || summaryDegraded,
			ContentHashAlgo: "jcs-sha256-v1",
			CoreVersion:     &coreV,
			GeneratedAt:     &genAt,
			Repos:           repoDigests,
		}
		d.ContentHash = render.ContentHash(d)

		// Rebuild the per-page view over the *digested* repos (with summaries) so
		// the renderer can section by requested page. Nil stays nil (replay /
		// fallback), which makes the renderer use the legacy layout.
		byPage := digestByPage(in.ByPage, repoDigests)

		// Index history: scan the checked-out branch for prior digests so index.md
		// accumulates instead of being rewritten to a single day. The repo is the
		// source of truth, so read from cfg.Repo.Path (dry runs may have no cfg).
		history, _ := render.ListDigests(historyRoot, limits)
		merged := mergeDigests(history, d)

		built, berr := render.BuildWithBanners(d, byPage, merged, limits, outDir, banners)
		if berr != nil {
			return res, berr
		}
		files = built
		// Remember which artifact is the digest itself. The CLI inlines its bytes
		// into RunResult so the consumer never has to read the output repository
		// (the branch may already be merged and deleted by the time it looks).
		digestName := render.NameOf(period, d.Date) + ".md"
		for _, f := range files {
			if strings.HasSuffix(f.Path, "/"+digestName) {
				res.DigestPath = f.Path
				break
			}
		}
	} // end wantsDigest

	// Optional repo-release subscription stage. Its artifacts are appended to
	// the same files list, so one commit/PR carries both trending and releases.
	var relState *releasewatch.State
	relStatePath := ""
	if opts.Releases != nil && cfg != nil && cfg.Releases.Enabled && len(opts.Releases.Repos) > 0 {
		rc := cfg.Releases
		relStart := time.Now()
		ro := releasewatch.Options{
			Repos:              opts.Releases.Repos,
			Date:               in.Date,
			Period:             rc.Period,
			Timezone:           rc.Timezone,
			WeekStart:          rc.WeekStart,
			IncludePrerelease:  rc.IncludePrerelease,
			IncludeDrafts:      rc.IncludeDrafts,
			FallbackToTags:     rc.FallbackToTagsEnabled(),
			BaselineOnFirstRun: rc.BaselineOnFirstRunEnabled(),
			BackfillLatest:     rc.BackfillLatest,
			MaxNotesChars:      rc.MaxNotesChars,
			SummaryMaxChars:    rc.SummaryMaxChars,
			MaxPages:           rc.MaxPages,
			// Auto resolves from the polled repo count, not from a cached number:
			// the same config then adapts as the subscription list grows (A13).
			Concurrency:   rc.Concurrency.Resolve(len(opts.Releases.Repos), 4, 8),
			BudgetSeconds: rc.BudgetSeconds.ResolveBudget(len(opts.Releases.Repos), 300),
			Provider:      provider,
			ProviderName:  cfg.Model.Provider,
			ModelName:     cfg.Model.Name,
			StatePath:     opts.Releases.StatePath,
			CachePath:     opts.Releases.CachePath,
			ReadOnly:      opts.DryRun,
		}
		drel, st, rrep, rerr := releasewatch.Build(ctx, opts.Releases.Fetcher, ro)
		relState, relStatePath = st, opts.Releases.StatePath
		rep.Releases = fromReleaseWatch(rrep)
		if rerr != nil {
			rep.Warnings = append(rep.Warnings, "releases: "+rerr.Error())
		} else {
			// The document date follows the summary period, so a mid-week run and
			// the Sunday run land on the same weekly files.
			d.Date = drel.Date
			d.Period = drel.Period
			if drel.Count > 0 {
				coreV := ipc.Version
				drel.CoreVersion = &coreV
				drel.GeneratedAt = &genAt
				drel.ContentHash = releasedigest.ContentHash(drel)
				hist, _ := render.ListReleaseDigests(historyRoot, rc.OutputDir, limits)
				mergedRel, todayRel := render.MergeReleaseDigests(hist, drel)
				relFiles, berr := render.BuildReleases(todayRel, mergedRel, limits, outDir, rc.OutputDir)
				if berr != nil {
					rep.Warnings = append(rep.Warnings, "releases render: "+berr.Error())
				} else {
					files = append(files, relFiles...)
				}
				// One markdown document per repo, week prepended, volume-split. The
				// content is the same summaryZh the weekly digest carries, so the
				// panel can render the file verbatim (A7).
				docFiles, derr := buildRepoDocs(cfg, outDir, rc, drel)
				if derr != nil {
					rep.Warnings = append(rep.Warnings, "releases docs: "+derr.Error())
				}
				files = append(files, docFiles...)
			}
		}
		rep.Stages = append(rep.Stages, Stage{Name: "releases", DurationMs: since(relStart)})
	} else if opts.Releases != nil {
		rep.Releases = &ReleaseReport{Enabled: cfg != nil && cfg.Releases.Enabled, Repos: len(opts.Releases.Repos)}
	}
	res.Digest = d

	res.Files = files

	if opts.DryRun {
		rep.TotalMs = since(start)
		res.Report = rep
		return res, nil
	}

	if cfg == nil {
		return res, fmt.Errorf("app: config required for a non-dry run")
	}
	if err := gitx.EnsureInstalled(); err != nil {
		return res, err
	}
	g := gitx.New(cfg.Repo.Path)
	if _, err := g.Toplevel(ctx); err != nil {
		return res, err
	}

	clean, err := g.IsClean(ctx)
	if err != nil {
		return res, err
	}
	if !clean {
		return res, &gitx.Error{Code: ipc.CodeRepoDirty, Message: "working tree has tracked changes"}
	}

	trigger := opts.Trigger
	if trigger == "" {
		trigger = TriggerManual
	}
	remote := cfg.Repo.Remote
	if remote == "" {
		remote = "origin"
	}
	base := cfg.Repo.BaseBranch
	if base == "" {
		base = "main"
	}
	gitStart := time.Now()
	if err := g.Fetch(ctx, remote); err != nil {
		return res, err
	}
	prefix := cfg.Output.BranchPrefix
	if prefix == "" {
		prefix = "trending/"
	}
	branch := prefix + period + "/" + name

	// PR lifecycle pre-check, before any git work:
	//   merged + schedule  -> the date is done, skip (automation must not re-open
	//                         a finished day, and the merged PR record persists in
	//                         GitHub even after the branch is deleted);
	//   merged + manual    -> explicit "regenerate this period" request: rebuild
	//                         and open a fresh PR. The old PR stays merged, so
	//                         openPR's FindOpen sees nothing and Create runs;
	//   closed + schedule  -> a human said no; an automated run must not reopen it;
	//   closed + manual    -> explicit request, rebuild/reopen.
	prRequested := cfg.PR.Enabled && opts.Token != ""
	var prClient prAPI
	if prRequested {
		c, cerr := newPRClientFor(ctx, g, cfg, opts.Token)
		if cerr != nil {
			rep.Warnings = append(rep.Warnings, "PR: "+cerr.Error())
		} else {
			latest, lerr := c.FindForBranch(ctx, branch, base)
			switch {
			case lerr != nil:
				rep.Warnings = append(rep.Warnings, "PR: "+lerr.Error())
			case latest.Merged() && trigger != TriggerManual:
				res.Skipped = true
				res.SkipReason = "merged"
				rep.TotalMs = since(start)
				res.Report = rep
				return res, nil
			case latest.ClosedUnmerged() && trigger == TriggerSchedule:
				res.Skipped = true
				res.SkipReason = "pr-closed"
				rep.TotalMs = since(start)
				res.Report = rep
				return res, nil
			default:
				// Includes merged + manual: rebuild the branch and let openPR open
				// a fresh PR (FindOpen returns nil for a merged/absent open PR).
				prClient = c
			}
		}
	}

	// Append-only branches: reuse what exists instead of resetting from the base.
	// A reset made every rerun a rewritten branch (hence the old force-with-lease);
	// appending means a plain push is always a fast-forward and nothing a human
	// committed on the branch is ever clobbered.
	hasLocal, err := g.HasRef(ctx, "refs/heads/"+branch)
	if err != nil {
		return res, err
	}
	hasRemote, err := g.HasRef(ctx, "refs/remotes/"+remote+"/"+branch)
	if err != nil {
		return res, err
	}
	switch {
	case hasLocal:
		if err := g.Switch(ctx, branch); err != nil {
			return res, err
		}
	case hasRemote:
		if err := g.SwitchCreate(ctx, branch, remote+"/"+branch); err != nil {
			return res, err
		}
	default:
		if err := g.SwitchCreate(ctx, branch, remote+"/"+base); err != nil {
			return res, err
		}
	}
	res.Branch = branch

	// Compute the repo-versioned subscription state on the branch we just
	// checked out. The subscription change never touched the working tree before
	// this point (subscribe writes .git/pending), so IsClean above saw a clean
	// tree. Now we replay pending onto the date branch and let it ride the same
	// commit as the artifacts.
	// A release-only run must not merge the pending subscription queue (A14): the
	// daily run owns that transition, and replaying it here would make a release
	// run rewrite subscriptions.yaml behind the trending PR.
	subsEnabled := wantsDigest && opts.Releases != nil && opts.Releases.SubscriptionsPath != ""
	var subsDirty bool
	if subsEnabled {
		// The checked-out branch == target branch here, so reading the working
		// file equals reading the branch version. Re-merge with pending so run is
		// the single place that lands the intent.
		baseSubs, err := subscription.Load(opts.Releases.SubscriptionsPath)
		if err != nil {
			return res, err
		}
		merged, _ := subscription.MergeUnique(baseSubs.List(), opts.Releases.PendingRepos)
		if opts.Releases.HasPending && !equalStrings(merged, baseSubs.List()) {
			if err := subscription.SaveList(opts.Releases.SubscriptionsPath, merged); err != nil {
				return res, err
			}
			subsDirty = true
		}
	}

	// The repo is the truth: if the branch already holds the same artifacts
	// (generatedAt aside), there is nothing to publish. This replaces the old
	// contentHash-vs-local-state skip, which drifted whenever the model reworded
	// a summary or the state file lived on another machine.
	artifactsUnchanged := filesMatch(files, cfg.Repo.Path)
	// A change to the subscription file must trigger a write + stage on its own,
	// even when the artifacts are byte-identical — otherwise "only the
	// subscription changed" would be silently dropped (review blocker #2).
	if !artifactsUnchanged || subsDirty {
		for _, f := range files {
			p := filepath.Join(cfg.Repo.Path, filepath.FromSlash(f.Path))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return res, err
			}
			if err := os.WriteFile(p, f.Data, 0o644); err != nil {
				return res, err
			}
		}
		// AddAll(outDir) covers both the rendered artifacts and the subscription
		// file (it lives inside outDir), so a subscription-only change is staged.
		if err := g.AddAll(ctx, outDir); err != nil {
			return res, err
		}
	}

	changed, err := g.HasStagedChanges(ctx)
	if err != nil {
		return res, err
	}
	pushed := false
	if changed {
		if err := g.Commit(ctx, commitMessage(cfg, period, name), cfg.Repo.AuthorName, cfg.Repo.AuthorEmail); err != nil {
			return res, err
		}
		if sha, err := g.HeadSHA(ctx); err == nil {
			res.Commit = sha
		}
		if err := pushWithRetry(ctx, g, remote, branch, opts.Token); err != nil {
			return res, pushFailure(err, branch, remote)
		}
		pushed = true
	}

	// Clear the pending queue only after commit + push both succeeded. If the
	// push failed above we returned early, so pending is preserved and replayed
	// by the next run — repeating a commit is acceptable, losing the subscription
	// change is not. When there was nothing dirty to commit (merged already equals
	// the branch, e.g. a duplicate add), the intent is already satisfied, so it is
	// safe to clear too.
	if subsEnabled && opts.Releases.HasPending {
		if pushed || !subsDirty {
			if err := subscription.ClearPending(cfg.Repo.Path); err != nil {
				rep.Warnings = append(rep.Warnings, "subscriptions: clear pending: "+err.Error())
			}
		}
	}
	rep.Stages = append(rep.Stages, Stage{Name: "git", DurationMs: since(gitStart)})

	// The PR is ensured even when the content did not change: a previous run may
	// have pushed and then failed to open the PR, and that must be retryable
	// without inventing a new commit.
	if prRequested && prClient != nil {
		url, werr := openPR(ctx, prClient, cfg, branch, d)
		if url != "" {
			res.PRURL = url
		}
		if werr != nil {
			rep.Warnings = append(rep.Warnings, "PR: "+werr.Error())
		}
		// Once the branch's PR is merged, drop the branch (remote + local) so the
		// repository keeps only the base branch. Only a confirmed merge triggers
		// this: an open (autoMerge off) or failed PR leaves its branch intact. A
		// cleanup failure only warns — the digest is already merged and delivered.
		if !opts.DryRun && res.PRURL != "" {
			if merged, verr := branchMerged(ctx, prClient, branch, cfg.Repo.BaseBranch); verr == nil && merged {
				if derr := prClient.DeleteBranch(ctx, branch); derr != nil {
					rep.Warnings = append(rep.Warnings, "branch cleanup (remote): "+derr.Error())
				}
				// Delete the local branch too; must switch off it first because git
				// refuses to delete the current branch.
				if serr := g.Switch(ctx, cfg.Repo.BaseBranch); serr == nil {
					if derr := g.DeleteLocalBranch(ctx, branch); derr != nil {
						rep.Warnings = append(rep.Warnings, "branch cleanup (local): "+derr.Error())
					}
				} else {
					rep.Warnings = append(rep.Warnings, "branch cleanup (switch to base): "+serr.Error())
				}
			}
		}
	}

	if relState != nil && relStatePath != "" && !opts.DryRun {
		if err := relState.Save(relStatePath); err != nil {
			rep.Warnings = append(rep.Warnings, "release-state: "+err.Error())
		}
	}

	res.Skipped = !changed
	if res.Skipped {
		res.SkipReason = "unchanged"
	}
	rep.TotalMs = since(start)
	res.Report = rep
	return res, nil
}

// filesMatch reports whether every rendered artifact already exists on the
// checked-out branch with the same comparable content (generatedAt aside).
func filesMatch(files []render.File, repoPath string) bool {
	for _, f := range files {
		p := filepath.Join(repoPath, filepath.FromSlash(f.Path))
		old, err := os.ReadFile(p)
		if err != nil {
			return false
		}
		if !bytes.Equal(render.Comparable(f.Path, old), render.Comparable(f.Path, f.Data)) {
			return false
		}
	}
	return true
}

// pushWithRetry pushes and, when the remote moved under us, rebases once onto
// the remote branch and retries. It never forces: the branch is append-only, so
// a rejected push means "someone else appended first", not "rewrite history".
func pushWithRetry(ctx context.Context, g *gitx.Git, remote, branch, token string) error {
	err := g.Push(ctx, remote, branch, token)
	if err == nil || !gitx.PushRejected(err) {
		return err
	}
	if ferr := g.Fetch(ctx, remote); ferr != nil {
		return err
	}
	if rerr := g.PullRebase(ctx, remote, branch); rerr != nil {
		// A rebase conflict is the real blocker (two writers touched the same
		// file); surface it instead of the now-stale push rejection.
		return rerr
	}
	return g.Push(ctx, remote, branch, token)
}

// pushFailure adds machine-readable context to a push error so the plugin can
// render an explanation without parsing git's prose.
func pushFailure(err error, branch, remote string) error {
	var ge *gitx.Error
	if !errors.As(err, &ge) {
		return err
	}
	ge.Details = map[string]any{
		"stage":  "push",
		"branch": branch,
		"remote": remote,
		"reason": gitx.ClassifyPushFailure(ge.Message),
	}
	return ge
}

// prAPI is the subset of pr.Client used when opening/merging a PR. It is
// indirected so tests can assert auto-merge without hitting GitHub.
type prAPI interface {
	FindOpen(ctx context.Context, head, base string) (*pr.Pull, error)
	FindForBranch(ctx context.Context, head, base string) (*pr.Pull, error)
	Create(ctx context.Context, title, head, base, bodyText string) (*pr.Pull, error)
	Update(ctx context.Context, number int, title, bodyText string) (*pr.Pull, error)
	Merge(ctx context.Context, number int) error
	DeleteBranch(ctx context.Context, branch string) error
}

var newPRClient = func(owner, repo, token string) prAPI { return pr.New(owner, repo, token) }

// newPRClientFor builds a PR client from the configured git remote. Indirected
// so tests can inject a fake without a real GitHub remote.
var newPRClientFor = func(ctx context.Context, g *gitx.Git, cfg *config.Config, token string) (prAPI, error) {
	remoteURL, err := g.RemoteURL(ctx, cfg.Repo.Remote)
	if err != nil {
		return nil, err
	}
	owner, repo, err := pr.ParseRemote(remoteURL)
	if err != nil {
		return nil, err
	}
	return newPRClient(owner, repo, token), nil
}

// branchMerged reports whether the most recent PR for branch->base is merged.
// Used after openPR to decide whether the branch can be cleaned up. A lookup
// error is returned so the caller can skip cleanup (leave the branch intact)
// rather than delete it on uncertainty.
func branchMerged(ctx context.Context, client prAPI, branch, base string) (bool, error) {
	latest, err := client.FindForBranch(ctx, branch, base)
	if err != nil {
		return false, err
	}
	return latest.Merged(), nil
}

func openPR(ctx context.Context, client prAPI, cfg *config.Config, branch string, d digest.Digest) (string, error) {
	period := render.PeriodOf(d)
	title := fmt.Sprintf("trending %s %s", period, d.Date)
	bodyText := fmt.Sprintf("automated %s trending digest for %s (%d repos).", period, d.Date, d.Count)

	existing, err := client.FindOpen(ctx, branch, cfg.Repo.BaseBranch)
	if err != nil {
		return "", err
	}
	if existing == nil {
		p, err := client.Create(ctx, title, branch, cfg.Repo.BaseBranch, bodyText)
		if err != nil {
			return "", err
		}
		// A freshly created PR must also honour autoMerge. Previously only the
		// "existing PR" path merged, so the first PR of a run was left open.
		if cfg.PR.AutoMerge {
			if err := client.Merge(ctx, p.Number); err != nil {
				return p.HTMLURL, err
			}
		}
		return p.HTMLURL, nil
	}
	p, err := client.Update(ctx, existing.Number, title, bodyText)
	if err != nil {
		return "", err
	}
	if cfg.PR.AutoMerge {
		if err := client.Merge(ctx, existing.Number); err != nil {
			return p.HTMLURL, err
		}
	}
	return p.HTMLURL, nil
}

func commitMessage(cfg *config.Config, period, name string) string {
	msg := strings.TrimSpace(cfg.Output.CommitMessage)
	if msg == "" {
		return "chore(trending): " + period + " " + name
	}
	if strings.Contains(msg, "%s") {
		return fmt.Sprintf(msg, name)
	}
	return msg
}

// mergeDigests merges today's digest into the history for the index, deduping by
// period+date so a same-day rerun updates a row instead of adding a duplicate.
// today wins on a key collision.
func mergeDigests(history []digest.Digest, today digest.Digest) []digest.Digest {
	key := func(d digest.Digest) string { return render.PeriodOf(d) + "\x00" + d.Date }
	seen := map[string]bool{key(today): true}
	out := make([]digest.Digest, 0, len(history)+1)
	out = append(out, today)
	for _, d := range history {
		k := key(d)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, d)
	}
	return out
}

func languagesOf(repos []digest.RepoSnapshot) []string {
	seen := map[string]bool{"all": true}
	out := []string{"all"}
	for _, r := range repos {
		if !seen[r.Language] {
			seen[r.Language] = true
			out = append(out, r.Language)
		}
	}
	return out
}

// ingestDegradation turns an ingest outcome into the markdown banner and the run
// warning shown on the runs page. The optional "（原因：…）" suffix is what makes a
// degraded run diagnosable: without it, "抓取失败" cannot tell a GitHub rate
// limit from a proxy timeout, and the two call for different responses.
func ingestDegradation(reason string, langs []string, detail string) (banner, warning string) {
	suffix := ""
	if detail != "" {
		suffix = "（原因：" + detail + "）"
	}
	switch reason {
	case ingest.ReasonSearchFallback:
		return "> ⚠️ 本次为降级数据：趋势页抓取失败，已回退到 GitHub 搜索 API。搜索结果无「今日新增」增量（starsInPeriod 一律为 0），排序按总 star，仅供参考。\n",
			"趋势页抓取失败，已回退到 GitHub 搜索 API：无今日新增增量（全部为 0），排序按总 star" + suffix
	case ingest.ReasonHTMLPartial:
		list := "未知"
		if len(langs) > 0 {
			list = strings.Join(langs, ", ")
		}
		return fmt.Sprintf("> ⚠️ 本次为部分降级数据：以下语言抓取失败，未包含在本次榜单中：%s。其余语言为正常趋势数据。\n", list),
			fmt.Sprintf("以下语言抓取失败，已跳过：%s%s", list, suffix)
	}
	return "", ""
}

// digestLanguages decides the digest's `languages` front matter. It prefers the
// caller's requested list (echoed verbatim, in order) so the document matches
// what the user actually selected rather than being reverse-derived from the
// repos that happened to surface. On the --input replay path (Requested empty)
// it falls back to languagesOf for backward compatibility.
func digestLanguages(in Inputs) []string {
	if len(in.Requested) > 0 {
		return append([]string(nil), in.Requested...)
	}
	return languagesOf(in.Repos)
}

// digestByPage re-keys the per-page snapshot view onto the summarized digest
// repos so the renderer can section by requested page while still carrying each
// repo's SummaryZh. Returns nil when there is no page information (replay /
// search fallback), which tells the renderer to use its legacy layout.
func digestByPage(byPage map[string][]digest.RepoSnapshot, digests []digest.RepoDigest) map[string][]digest.RepoDigest {
	if byPage == nil {
		return nil
	}
	byName := make(map[string]digest.RepoDigest, len(digests))
	for _, d := range digests {
		byName[d.FullName] = d
	}
	out := make(map[string][]digest.RepoDigest, len(byPage))
	for lang, repos := range byPage {
		list := make([]digest.RepoDigest, 0, len(repos))
		for _, r := range repos {
			if d, ok := byName[r.FullName]; ok {
				list = append(list, d)
				continue
			}
			// Should not happen (every snapshot gets a digest), but degrade
			// gracefully: render the snapshot with an empty summary rather than
			// dropping the row from its section.
			list = append(list, digest.RepoDigest{RepoSnapshot: r})
		}
		out[lang] = list
	}
	return out
}

func since(t time.Time) int64 { return time.Since(t).Milliseconds() }

// equalStrings reports whether two string slices are element-wise equal.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// buildRepoDocs plans the per-repo markdown documents for the repos that changed
// this week. It returns only files that need writing: a week already present in
// the document yields nothing, so a second run in the same week is a no-op (A4).
func buildRepoDocs(cfg *config.Config, outDir string, rc config.ReleasesConfig, d releasedigest.ReleaseDigest) ([]render.File, error) {
	if d.Count == 0 {
		return nil, nil
	}
	repoRoot := ""
	if cfg != nil {
		repoRoot = cfg.Repo.Path
	}
	docRoot := render.ReleaseRoot(outDir, rc.OutputDir)
	maxLines := rc.DocMaxLines
	if maxLines <= 0 {
		maxLines = releasedoc.DefaultMaxLines
	}
	listed := rc.PerRepoLimit
	if listed <= 0 {
		listed = releasedoc.MaxListedTags
	}
	out := make([]render.File, 0, len(d.Repos))
	for _, group := range d.Repos {
		sec := releasedoc.Section{
			Repo:      group.FullName,
			WeekStart: stringOf(group.PeriodStart),
			WeekEnd:   stringOf(group.PeriodEnd),
			SummaryZh: stringOf(group.SummaryZh),
			Degraded:  repoDegraded(group),
			Items:     docItems(group),
			MaxListed: listed,
		}
		file, data, need, err := releasedoc.Plan(repoRoot, docRoot, group.FullName, sec, maxLines)
		if err != nil {
			// One repo's document must never abort the week: a hand-edited file is
			// skipped with a warning and the rest of the run proceeds.
			return out, fmt.Errorf("%s: %w", group.FullName, err)
		}
		if !need {
			continue
		}
		out = append(out, render.File{Path: file, Data: data})
	}
	return out, nil
}

// docItems renders the bullet list source for one repo's week, newest first.
func docItems(g releasedigest.RepoReleases) []releasedoc.Item {
	items := make([]releasedoc.Item, 0, len(g.Releases))
	for _, it := range g.Releases {
		date := ""
		if it.PublishedAt != nil {
			date = it.PublishedAt.UTC().Format("2006-01-02")
		}
		items = append(items, releasedoc.Item{Tag: it.Tag, URL: it.URL, Date: date})
	}
	return items
}

// repoDegraded reports whether this repo's week fell back to raw notes.
func repoDegraded(g releasedigest.RepoReleases) bool {
	for _, it := range g.Releases {
		if it.SummarySource == "original" {
			return true
		}
	}
	return false
}

func stringOf(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
