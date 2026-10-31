package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/morehao/trendradar/internal/app"
	"github.com/morehao/trendradar/internal/config"
	"github.com/morehao/trendradar/internal/fetch"
	"github.com/morehao/trendradar/internal/gitx"
	"github.com/morehao/trendradar/internal/ingest"
	"github.com/morehao/trendradar/internal/ipc"
	"github.com/morehao/trendradar/internal/model"
)

// runRun implements `trendradar run`. In --json mode it always emits a contract
// RunResult, including on failure (ok=false + error ErrorInfo).
func runRun(args []string) error {
	started := time.Now()
	var asJSON bool
	err := runRunExec(args, started, &asJSON)
	if err != nil && asJSON {
		out := ipc.RunResult{
			IPCSchema:  ipc.SchemaVersion,
			OK:         false,
			Command:    string(ipc.CommandRun),
			StartedAt:  started.UTC(),
			FinishedAt: time.Now().UTC(),
		}
		var ie *ipc.ErrorInfo
		var ge *gitx.Error
		switch {
		case errors.As(err, &ie):
			out.Error = ie.ToContract()
		case errors.As(err, &ge):
			e := ipc.NewError(ge.Code, "%s", ge.Message)
			e.Details = ge.Details
			out.Error = e.ToContract()
		default:
			out.Error = ipc.NewError(ipc.CodeInternal, "%s", err.Error()).ToContract()
		}
		b, _ := json.Marshal(out)
		fmt.Println(string(b))
	}
	return err
}

// runEnvelope carries the contract RunResult plus fields the contract does not
// type yet (RunResult declares additionalProperties, so this stays compatible).
type runEnvelope struct {
	ipc.RunResult
	SkipReason string `json:"skipReason,omitempty"`
}

func runRunExec(args []string, started time.Time, asJSON *bool) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to config.yaml")
	input := fs.String("input", "", "snapshots JSON file ([]RepoSnapshot); skips network fetch")
	date := fs.String("date", time.Now().Format("2006-01-02"), "digest date (YYYY-MM-DD)")
	dryRun := fs.Bool("dry-run", false, "do not write files or touch git")
	jsonFlag := fs.Bool("json", false, "emit machine-readable JSON")
	outDir := fs.String("out", "", "output directory (overrides config.output.dir)")
	period := fs.String("period", "", "daily|weekly|monthly (overrides config.trending.since)")
	defaultTrigger := firstNonEmpty(os.Getenv("TRENDRADAR_TRIGGER"), string(app.TriggerManual))
	trigger := fs.String("trigger", defaultTrigger, "manual|schedule; schedule respects a human-closed PR")
	if err := fs.Parse(args); err != nil {
		return err
	}
	*asJSON = *jsonFlag
	tr := app.Trigger(*trigger)
	if tr != app.TriggerManual && tr != app.TriggerSchedule {
		return ipc.NewError(ipc.CodeConfigInvalid, "invalid --trigger %q (want manual|schedule)", *trigger)
	}

	var cfg *config.Config
	if *cfgPath != "" {
		c, err := config.Load(*cfgPath)
		if err != nil {
			return err
		}
		cfg = c
	}
	if *outDir != "" {
		if cfg == nil {
			cfg = &config.Config{}
		}
		cfg.Output.Dir = *outDir
	}
	// --period overrides the config's trending.since so "what we fetch" and
	// "what we label/store" stay in sync. Validate loudly instead of silently
	// falling back.
	if *period != "" {
		switch *period {
		case "daily", "weekly", "monthly":
		default:
			return ipc.NewError(ipc.CodeConfigInvalid, "invalid --period %q (want daily|weekly|monthly)", *period)
		}
		if cfg == nil {
			cfg = &config.Config{}
		}
		cfg.Trending.Since = *period
	}

	in := app.Inputs{Date: *date}
	if *input != "" {
		raw, err := os.ReadFile(*input)
		if err != nil {
			return fmt.Errorf("read input: %w", err)
		}
		if err := json.Unmarshal(raw, &in.Repos); err != nil {
			return fmt.Errorf("parse snapshots: %w", err)
		}
	} else {
		if cfg == nil {
			return fmt.Errorf("--config is required when --input is not given")
		}
		client := fetch.NewClient(fetch.Options{})
		res, err := ingest.Ingest(context.Background(), client, cfg.Trending.Languages, cfg.Trending.Since, cfg.Trending.PerLanguage)
		if err != nil {
			return err
		}
		in.Repos, in.Degraded = res.Repos, res.Degraded
	}
	if len(in.Repos) == 0 {
		return fmt.Errorf("no repository snapshots to process")
	}
	if cfg != nil && cfg.Trending.Since != "" {
		in.Period = cfg.Trending.Since
	}

	provider, err := selectProvider(cfg)
	if err != nil {
		return err
	}
	result, err := app.Run(context.Background(), cfg, in, app.Options{
		DryRun:   *dryRun,
		Token:    firstNonEmpty(os.Getenv("GITHUB_TOKEN"), os.Getenv("GH_TOKEN")),
		Provider: provider,
		Trigger:  tr,
	})
	if err != nil {
		return err
	}
	for _, w := range result.Report.Warnings {
		fmt.Fprintf(os.Stderr, "warn: %s\n", w)
	}

	// Persist per-run token usage for later aggregation. Dry runs and non-git
	// targets are skipped; a logging failure never fails the run.
	if !*dryRun && result.Report.Usage != nil {
		repoPath := ""
		providerName, modelName := "", ""
		if cfg != nil {
			repoPath = cfg.Repo.Path
			providerName, modelName = cfg.Model.Provider, cfg.Model.Name
		}
		u := result.Report.Usage
		rec := app.UsageRecord{
			Time:             time.Now().UTC(),
			Provider:         providerName,
			Model:            modelName,
			ContentHash:      result.Digest.ContentHash,
			PromptTokens:     u.PromptTokens,
			CompletionTokens: u.CompletionTokens,
			TotalTokens:      u.TotalTokens,
			CachedTokens:     u.CachedTokens,
			UsageKnown:       u.UsageKnown,
			Calls:            u.Calls,
			Retries:          u.Retries,
			Batches:          u.Batches,
			CacheHits:        u.CacheHits,
		}
		if err := app.AppendUsageLog(repoPath, rec); err != nil {
			fmt.Fprintf(os.Stderr, "warn: usage log: %v\n", err)
		}
	}

	if *asJSON {
		out := runEnvelope{
			RunResult: ipc.RunResult{
				IPCSchema:  ipc.SchemaVersion,
				OK:         true,
				Command:    string(ipc.CommandRun),
				StartedAt:  started.UTC(),
				FinishedAt: time.Now().UTC(),
				Skipped:    result.Skipped,
				Branch:     strPtr(result.Branch),
				Commit:     strPtr(result.Commit),
				PRUrl:      strPtr(result.PRURL),
				Warnings:   result.Report.Warnings,
				Usage:      result.Report.Usage,
			},
			SkipReason: result.SkipReason,
		}
		if result.Digest.ContentHash != "" {
			ch := result.Digest.ContentHash
			out.ContentHash = &ch
		}
		for _, f := range result.Files {
			sum := sha256.Sum256(f.Data)
			out.Files = append(out.Files, ipc.FileArtifact{Path: f.Path, Bytes: len(f.Data), SHA256: "sha256:" + hex.EncodeToString(sum[:])})
		}
		b, _ := json.Marshal(out)
		fmt.Println(string(b))
		return nil
	}

	if result.Skipped {
		fmt.Printf("skipped (%s): %s\n", result.SkipReason, result.Digest.ContentHash)
		if u := result.Report.Usage; u != nil && u.UsageKnown {
			fmt.Printf("  tokens: prompt=%d completion=%d total=%d (calls=%d retries=%d cacheHits=%d)\n",
				u.PromptTokens, u.CompletionTokens, u.TotalTokens, u.Calls, u.Retries, u.CacheHits)
		}
		return nil
	}
	fmt.Printf("digest %s (%s): %d repos, %d files, contentHash=%s\n",
		result.Digest.Date, result.Digest.Period, result.Digest.Count, len(result.Files), result.Digest.ContentHash)
	if result.Branch != "" {
		fmt.Printf("  branch: %s\n", result.Branch)
	}
	if result.PRURL != "" {
		fmt.Printf("  PR: %s\n", result.PRURL)
	}
	for _, s := range result.Report.Stages {
		fmt.Printf("  %-10s %dms\n", s.Name, s.DurationMs)
	}
	if u := result.Report.Usage; u != nil && u.UsageKnown {
		fmt.Printf("  tokens: prompt=%d completion=%d total=%d (calls=%d retries=%d cacheHits=%d)\n",
			u.PromptTokens, u.CompletionTokens, u.TotalTokens, u.Calls, u.Retries, u.CacheHits)
	}
	return nil
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func selectProvider(cfg *config.Config) (model.Provider, error) {
	if cfg == nil || cfg.Model.Provider == "" || cfg.Model.Provider == "fake" {
		return model.NewFake(), nil
	}
	return model.NewGLLM(model.GLLMOptions{
		Provider:      cfg.Model.Provider,
		ModelName:     cfg.Model.Name,
		BaseURL:       cfg.Model.BaseURL,
		APIKey:        cfg.Model.APIKey,
		Temperature:   cfg.Model.Temperature,
		MaxTokens:     cfg.Model.MaxTokens,
		AllowDegraded: cfg.Model.AllowDegraded,
	})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
