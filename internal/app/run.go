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
	"github.com/morehao/trendradar/internal/gitx"
	"github.com/morehao/trendradar/internal/ipc"
	"github.com/morehao/trendradar/internal/model"
	"github.com/morehao/trendradar/internal/pr"
	"github.com/morehao/trendradar/internal/render"
	"github.com/morehao/trendradar/internal/summarize"
)

// Inputs are the snapshots for a run (already fetched/parsed).
type Inputs struct {
	Repos    []digest.RepoSnapshot
	Degraded bool
	Date     string
	Period   string
}

// Trigger says who asked for the run. It only changes the PR lifecycle policy:
// an automated run respects a human closing the PR, while a manual run is an
// explicit request to re-open it.
type Trigger string

const (
	TriggerManual   Trigger = "manual"
	TriggerSchedule Trigger = "schedule"
)

// Options controls a run.
type Options struct {
	DryRun   bool
	Token    string         // optional GitHub token for push/PR
	Provider model.Provider // optional; defaults to the offline Fake
	Trigger  Trigger        // optional; defaults to TriggerManual
}

// Result is the outcome of a run.
type Result struct {
	Digest     digest.Digest
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

	sumStart := time.Now()
	// Persisted summary cache: without it every run re-asks the model, the
	// rendered text drifts, and "same input" never yields the same digest.
	// The cache is keyed by repo+description+prompt/model namespace, so a model
	// or prompt change misses instead of silently reusing stale prose.
	cachePath := ""
	if cfg != nil && !opts.DryRun && cfg.Repo.Path != "" {
		cachePath = summarize.CachePath(cfg.Repo.Path)
	}
	summaryCache, _ := summarize.LoadCache(cachePath)
	if summaryCache == nil {
		summaryCache = summarize.NewCache()
	}
	if cfg != nil {
		summaryCache.SetNamespace(summarize.NamespaceForMode(cfg.Model.Provider, cfg.Model.Name, "none"))
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
	repoDigests := summarizer.SummarizeWithContext(ctx, in.Repos, nil)
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
	rep.Stages = append(rep.Stages, Stage{Name: "summarize", DurationMs: since(sumStart), Degraded: summaryDegraded})
	if summaryDegraded {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf(
			"%d/%d 仓库摘要降级为英文原文（模型不可用或返回无效结果）", degradedCount, len(repoDigests)))
	}

	// The digest root must be identical for rendering and `git add`; compute it
	// once here so output.dir is honoured end-to-end (dry runs have no cfg).
	outDir := render.OutputRoot("")
	if cfg != nil {
		outDir = render.OutputRoot(cfg.Output.Dir)
	}

	// Normalize the period here so both the digest and the branch/artifact paths
	// agree (PeriodOf maps anything outside daily|weekly|monthly to daily).
	period := render.PeriodOf(digest.Digest{Period: in.Period})
	coreV := ipc.Version
	genAt := time.Now().UTC()
	d := digest.Digest{
		Schema:          2,
		Date:            in.Date,
		Period:          period,
		Languages:       languagesOf(in.Repos),
		Count:           len(repoDigests),
		Degraded:        in.Degraded || summaryDegraded,
		ContentHashAlgo: "jcs-sha256-v1",
		CoreVersion:     &coreV,
		GeneratedAt:     &genAt,
		Repos:           repoDigests,
	}
	d.ContentHash = render.ContentHash(d)
	name := render.NameOf(period, d.Date)

	// Index history: scan the checked-out branch for prior digests so index.md
	// accumulates instead of being rewritten to a single day. The repo is the
	// source of truth, so read from cfg.Repo.Path (dry runs may have no cfg).
	limits := map[string]int{"daily": 90, "weekly": 12, "monthly": 12}
	historyRoot := outDir
	if cfg != nil && cfg.Repo.Path != "" {
		historyRoot = filepath.Join(cfg.Repo.Path, outDir)
	}
	history, _ := render.ListDigests(historyRoot, limits)
	merged := mergeDigests(history, d)

	files, err := render.Build(d, merged, limits, outDir)
	if err != nil {
		return res, err
	}
	res.Digest, res.Files = d, files

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
	//   merged            -> the date is done, skip;
	//   closed + schedule -> a human said no; an automated run must not reopen it;
	//   closed + manual   -> explicit request, rebuild/reopen.
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
			case latest.Merged():
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

	// The repo is the truth: if the branch already holds the same artifacts
	// (generatedAt aside), there is nothing to publish. This replaces the old
	// contentHash-vs-local-state skip, which drifted whenever the model reworded
	// a summary or the state file lived on another machine.
	unchanged := filesMatch(files, cfg.Repo.Path)
	if !unchanged {
		for _, f := range files {
			p := filepath.Join(cfg.Repo.Path, filepath.FromSlash(f.Path))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return res, err
			}
			if err := os.WriteFile(p, f.Data, 0o644); err != nil {
				return res, err
			}
		}
		if err := g.AddAll(ctx, outDir); err != nil {
			return res, err
		}
	}

	changed := false
	if !unchanged {
		changed, err = g.HasStagedChanges(ctx)
		if err != nil {
			return res, err
		}
	}
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

func since(t time.Time) int64 { return time.Since(t).Milliseconds() }
