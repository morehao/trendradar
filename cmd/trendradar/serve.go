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
			if cfg.Output.Dir != "" {
				*root = cfg.Output.Dir
			}
			// Expose the subscription surface only when configured; otherwise the
			// releases routes still answer with empty lists.
			if cfg.Releases.Enabled {
				relSubdir = cfg.Releases.OutputDir
				if p, err := subscription.ResolveSubscriptionsPath(*cfgPath, cfg.Releases.SubscriptionsFile); err == nil {
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
