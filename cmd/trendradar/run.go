package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/morehao/trendradar/internal/config"
	"github.com/morehao/trendradar/internal/digest"
	"github.com/morehao/trendradar/internal/ipc"
	"github.com/morehao/trendradar/internal/model"
	"github.com/morehao/trendradar/internal/render"
	"github.com/morehao/trendradar/internal/summarize"
)

type fileOut struct {
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type dryOutput struct {
	Files  []fileOut `json:"files"`
	Digest struct {
		ContentHash string `json:"contentHash"`
	} `json:"digest"`
}

// runRun implements `trendradar run`. In M3 it renders from a snapshots JSON
// file (--input) using either the offline fake or gllm provider.
func runRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to config.yaml")
	input := fs.String("input", "", "snapshots JSON file ([]RepoSnapshot)")
	date := fs.String("date", time.Now().Format("2006-01-02"), "digest date (YYYY-MM-DD)")
	dryRun := fs.Bool("dry-run", false, "do not write files")
	asJSON := fs.Bool("json", false, "emit machine-readable JSON")
	outDir := fs.String("out", "", "output directory (overrides config)")
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

	if *input == "" {
		return fmt.Errorf("--input is required in M3 (network fetch is wired in M4)")
	}
	raw, err := os.ReadFile(*input)
	if err != nil {
		return fmt.Errorf("read input: %w", err)
	}
	var repos []digest.RepoSnapshot
	if err := json.Unmarshal(raw, &repos); err != nil {
		return fmt.Errorf("parse snapshots: %w", err)
	}
	if len(repos) == 0 {
		return fmt.Errorf("no snapshots in %s", *input)
	}

	provider, err := selectProvider(cfg)
	if err != nil {
		return err
	}

	summarizer := summarize.New(provider, summarize.WithCache(summarize.NewCache()))
	repoDigests := summarizer.Summarize(context.Background(), repos)

	period := "daily"
	if cfg != nil && cfg.Trending.Since != "" {
		period = cfg.Trending.Since
	}
	d := digest.Digest{
		Schema:      1,
		Date:        *date,
		Period:      period,
		Languages:   languagesOf(repos),
		Count:       len(repoDigests),
		Degraded:    false,
		CoreVersion: ipc.Version,
		GeneratedAt: time.Now().UTC(),
		Repos:       repoDigests,
	}
	d.ContentHash = render.ContentHash(d)

	files, err := render.Build(d, []digest.Digest{d}, 90)
	if err != nil {
		return err
	}

	if !*dryRun {
		base := *outDir
		if base == "" && cfg != nil {
			base = cfg.Output.Dir
		}
		if base == "" {
			base = "."
		}
		for _, f := range files {
			path := filepath.Join(base, filepath.FromSlash(f.Path))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, f.Data, 0o644); err != nil {
				return err
			}
		}
	}

	if *asJSON {
		out := dryOutput{}
		out.Digest.ContentHash = d.ContentHash
		for _, f := range files {
			sum := sha256.Sum256(f.Data)
			out.Files = append(out.Files, fileOut{
				Path:   f.Path,
				Bytes:  len(f.Data),
				SHA256: "sha256:" + hex.EncodeToString(sum[:]),
			})
		}
		b, _ := json.Marshal(out)
		fmt.Println(string(b))
		return nil
	}

	fmt.Printf("digest %s (%s): %d repos, %d files, contentHash=%s\n", d.Date, d.Period, d.Count, len(files), d.ContentHash)
	for _, f := range files {
		fmt.Printf("  %s (%d bytes)\n", f.Path, len(f.Data))
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

func languagesOf(repos []digest.RepoSnapshot) []string {
	seen := map[string]bool{}
	out := []string{"all"}
	seen["all"] = true
	for _, r := range repos {
		if !seen[r.Language] {
			seen[r.Language] = true
			out = append(out, r.Language)
		}
	}
	return out
}
