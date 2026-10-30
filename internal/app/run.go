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
	rep.Stages = append(rep.Stages, Stage{Name: "summarize", DurationMs: since(sumStart)})

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
		Degraded:    in.Degraded,
		CoreVersion: ipc.Version,
		GeneratedAt: time.Now().UTC(),
		Repos:       repoDigests,
	}
	d.ContentHash = render.ContentHash(d)

	files, err := render.Build(d, []digest.Digest{d}, 90)
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
	if st.LastRun != nil && st.LastRun.ContentHash == d.ContentHash && st.LastRun.Date == d.Date {
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

	outDir := cfg.Output.Dir
	if outDir == "" {
		outDir = "digests"
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
	branch := cfg.Output.BranchPrefix + d.Date
	if cfg.Output.BranchPrefix == "" {
		branch = "trending/" + d.Date
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
	if err := g.Push(ctx, remote, branch, opts.Token); err != nil {
		return res, err
	}
	res.Branch = branch
	rep.Stages = append(rep.Stages, Stage{Name: "git", DurationMs: since(gitStart)})

	st.LastRun = &ipc.StateRun{Date: d.Date, ContentHash: d.ContentHash, RunAt: time.Now().UTC()}
	if err := state.Save(cfg.Repo.Path, st); err != nil {
		return res, err
	}

	if cfg.PR.Enabled && opts.Token != "" {
		url, werr := openPR(ctx, g, cfg, opts.Token, branch, d)
		if werr != nil {
			rep.Warnings = append(rep.Warnings, "PR: "+werr.Error())
		} else {
			res.PRURL = url
		}
	}

	rep.TotalMs = since(start)
	res.Report = rep
	return res, nil
}

func openPR(ctx context.Context, g *gitx.Git, cfg *config.Config, token, branch string, d digest.Digest) (string, error) {
	remoteURL, err := g.RemoteURL(ctx, cfg.Repo.Remote)
	if err != nil {
		return "", err
	}
	owner, repo, err := pr.ParseRemote(remoteURL)
	if err != nil {
		return "", err
	}
	client := pr.New(owner, repo, token)
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
