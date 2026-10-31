package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/morehao/trendradar/internal/config"
	"github.com/morehao/trendradar/internal/render"
	"github.com/morehao/trendradar/internal/serve"
	"github.com/morehao/trendradar/internal/subscription"
)

// runServe implements `trendradar serve`.
func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8787", "listen address")
	cfgPath := fs.String("config", "", "config.yaml (uses output.dir as digest root)")
	root := fs.String("root", "digests", "digests directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	relSubdir, subsPath := "", ""
	if *cfgPath != "" {
		if cfg, err := config.Load(*cfgPath); err == nil {
			// The digests root must be normalised through render.OutputRoot — the
			// same single source of truth run uses — so serve's /api/v2/releases*
			// and subscription routes point at the same directory as the artifacts
			// run writes (cfg.Output.Dir may be "" meaning "digests").
			rootVal := render.OutputRoot(cfg.Output.Dir)
			*root = rootVal
			// Expose the subscription surface only when configured; otherwise the
			// releases routes still answer with empty lists.
			if cfg.Releases.Enabled {
				relSubdir = cfg.Releases.OutputDir
				// Read the SAME versioned file run writes: repo + output.dir, not the
				// config directory (the old resolver made serve and run disagree).
				if p, err := subscription.RepoSubscriptionsPath(cfg.Repo.Path, cfg.Output.Dir, cfg.Releases.SubscriptionsFile); err == nil {
					subsPath = p
				}
			}
		}
	}

	server := serve.New(serve.NewStore(*root)).WithReleases(relSubdir, subsPath)
	httpSrv := &http.Server{Addr: *addr, Handler: server.Handler()}

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	}()

	fmt.Printf("trendradar serve on http://%s (digests root %s)\n", *addr, *root)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
