// Package app orchestrates a full run: summarize, render, commit, push and
// (optionally) open a pull request.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/morehao/trendradar/internal/config"
	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/gitx"
	"github.com/morehao/trendradar/internal/ipc"
	"github.com/morehao/trendradar/internal/model"
	"github.com/morehao/trendradar/internal/pr"
	"github.com/morehao/trendradar/internal/render"
	"github.com/morehao/trendradar/internal/state"
	"github.com/morehao/trendradar/internal/summarize"
)

// Inputs are the snapshots for a run (already fetched/parsed).
type Inputs struct {
	Repos    []digest.RepoSnapshot
	Degraded bool
	Date     string
	Period   string
}

// Options controls a run.
type Options struct {
	DryRun   bool
	Token    string         // optional GitHub token for push/PR
	Provider model.Provider // optional; defaults to the offline Fake
}

// Result is the outcome of a run.
type Result struct {
	Digest  digest.Digest
	Files   []render.File
	Report  Report
	Skipped bool
	Branch  string
	Commit  string
	PRURL   string
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
	repoDigests := summarize.New(provider, summarize.WithCache(summarize.NewCache())).Summarize(ctx, in.Repos)
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

	period := in.Period
	if period == "" {
		period = "daily"
	}
	d := digest.Digest{
		Schema:      1,
		Date:        in.Date,
		Period:      period,
		Languages:   languagesOf(in.Repos),
		Count:       len(repoDigests),
		Degraded:    in.Degraded || summaryDegraded,
		CoreVersion: ipc.Version,
		GeneratedAt: time.Now().UTC(),
		Repos:       repoDigests,
	}
	d.ContentHash = render.ContentHash(d)

	files, err := render.Build(d, []digest.Digest{d}, 90, outDir)
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

	st, err := state.Load(cfg.Repo.Path)
	if err != nil {
		return res, err
	}
	// Idempotency key is date + contentHash + output.dir. Including the output
	// directory means changing only output.dir re-renders instead of being
	// silently skipped as "same content".
	if st.LastRun != nil && st.LastRun.ContentHash == d.ContentHash && st.LastRun.Date == d.Date && st.LastRun.OutputDir == outDir {
		res.Skipped = true
		rep.TotalMs = since(start)
		res.Report = rep
		return res, nil
	}

	clean, err := g.IsClean(ctx)
	if err != nil {
		return res, err
	}
	if !clean {
		return res, &gitx.Error{Code: ipc.CodeRepoDirty, Message: "working tree has tracked changes"}
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
	branch := prefix + d.Date

	// PR lifecycle pre-check: decide from the most recent PR for this branch
	// whether the date is already done (merged -> skip) or was closed unmerged
	// (rebuild the branch and open a fresh PR). It runs before any git work so a
	// merged date never leaves a stray re-created branch behind.
	prRequested := cfg.PR.Enabled && opts.Token != ""
	prOK := true
	var prClient prAPI
	if prRequested {
		c, cerr := newPRClientFor(ctx, g, cfg, opts.Token)
		if cerr != nil {
			prOK = false
			rep.Warnings = append(rep.Warnings, "PR: "+cerr.Error())
		} else {
			latest, lerr := c.FindForBranch(ctx, branch, base)
			switch {
			case lerr != nil:
				prOK = false
				rep.Warnings = append(rep.Warnings, "PR: "+lerr.Error())
			case latest.Merged():
				// Already merged for this date: nothing to push or open.
				res.Skipped = true
				st.LastRun = &ipc.StateRun{Date: d.Date, ContentHash: d.ContentHash, OutputDir: outDir, RunAt: time.Now().UTC()}
				if err := state.Save(cfg.Repo.Path, st); err != nil {
					return res, err
				}
				rep.TotalMs = since(start)
				res.Report = rep
				return res, nil
			default:
				// open, closed-unmerged, or no PR: (re)build the branch below.
				// C1's force-with-lease push safely replaces a closed PR's stale
				// branch; an open PR is later updated by openPR.
				prClient = c
			}
		}
	}

	// Switch before writing: tracked files from a previous date would otherwise
	// block `git switch -C` with "local changes would be overwritten".
	if err := g.SwitchCreate(ctx, branch, remote+"/"+base); err != nil {
		return res, err
	}
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
	if has, _ := g.HasStagedChanges(ctx); !has {
		res.Skipped = true
		rep.TotalMs = since(start)
		res.Report = rep
		return res, nil
	}
	if err := g.Commit(ctx, commitMessage(cfg, d.Date), cfg.Repo.AuthorName, cfg.Repo.AuthorEmail); err != nil {
		return res, err
	}
	if gitx.IsOwnedBranch(branch, prefix) {
		// Own per-date branch rebuilt from base each run: force-with-lease (C1).
		if err := g.PushForceWithLease(ctx, remote, branch, opts.Token); err != nil {
			return res, err
		}
	} else if err := g.Push(ctx, remote, branch, opts.Token); err != nil {
		return res, err
	}
	res.Branch = branch
	rep.Stages = append(rep.Stages, Stage{Name: "git", DurationMs: since(gitStart)})

	if prRequested && prClient != nil {
		url, werr := openPR(ctx, prClient, cfg, branch, d)
		if url != "" {
			res.PRURL = url
		}
		if werr != nil {
			prOK = false
			rep.Warnings = append(rep.Warnings, "PR: "+werr.Error())
		}
	}

	// Persist state only once the whole run -- including the PR -- succeeded. If
	// the push worked but the PR failed, leaving state unsaved makes the next run
	// with the same content re-attempt instead of skipping and never retrying the
	// PR (C2).
	if prOK {
		st.LastRun = &ipc.StateRun{Date: d.Date, ContentHash: d.ContentHash, OutputDir: outDir, RunAt: time.Now().UTC()}
		if err := state.Save(cfg.Repo.Path, st); err != nil {
			return res, err
		}
	}

	rep.TotalMs = since(start)
	res.Report = rep
	return res, nil
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
	title := fmt.Sprintf("trending %s", d.Date)
	bodyText := fmt.Sprintf("automated trending digest for %s (%d repos).", d.Date, d.Count)

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

func commitMessage(cfg *config.Config, date string) string {
	msg := strings.TrimSpace(cfg.Output.CommitMessage)
	if msg == "" {
		return "chore(trending): " + date
	}
	if strings.Contains(msg, "%s") {
		return fmt.Sprintf(msg, date)
	}
	return msg
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
