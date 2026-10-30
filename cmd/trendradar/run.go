package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/morehao/trendradar/internal/app"
	"github.com/morehao/trendradar/internal/config"
	"github.com/morehao/trendradar/internal/fetch"
	"github.com/morehao/trendradar/internal/ingest"
	"github.com/morehao/trendradar/internal/model"
)

type fileOut struct {
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type runOutput struct {
	Files   []fileOut `json:"files"`
	Skipped bool      `json:"skipped"`
	Branch  string    `json:"branch,omitempty"`
	PRURL   string    `json:"prUrl,omitempty"`
	Digest  struct {
		ContentHash string `json:"contentHash"`
	} `json:"digest"`
}

// runRun implements `trendradar run`: ingest (network or --input) -> summarize
// -> render -> git commit/push (+ PR when enabled).
func runRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to config.yaml")
	input := fs.String("input", "", "snapshots JSON file ([]RepoSnapshot); skips network fetch")
	date := fs.String("date", time.Now().Format("2006-01-02"), "digest date (YYYY-MM-DD)")
	dryRun := fs.Bool("dry-run", false, "do not write files or touch git")
	asJSON := fs.Bool("json", false, "emit machine-readable JSON")
	outDir := fs.String("out", "", "output directory (overrides config.output.dir)")
	if err := fs.Parse(args); err != nil {
		return err
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
	})
	if err != nil {
		return err
	}
	for _, w := range result.Report.Warnings {
		fmt.Fprintf(os.Stderr, "warn: %s\n", w)
	}

	if *asJSON {
		out := runOutput{Skipped: result.Skipped, Branch: result.Branch, PRURL: result.PRURL}
		out.Digest.ContentHash = result.Digest.ContentHash
		for _, f := range result.Files {
			sum := sha256.Sum256(f.Data)
			out.Files = append(out.Files, fileOut{Path: f.Path, Bytes: len(f.Data), SHA256: "sha256:" + hex.EncodeToString(sum[:])})
		}
		b, _ := json.Marshal(out)
		fmt.Println(string(b))
		return nil
	}

	if result.Skipped {
		fmt.Printf("skipped: contentHash unchanged (%s), no commit\n", result.Digest.ContentHash)
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
	return nil
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
