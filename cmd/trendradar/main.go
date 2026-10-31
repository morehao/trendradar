package main

import (
	"fmt"
	"os"

	"github.com/morehao/trendradar/internal/envfile"
)

func main() {
	// Load local secrets (.env.local / .env) before parsing commands. Missing
	// files are fine; real environment variables keep priority.
	if path, err := envfile.ApplyDefault("."); err != nil {
		fmt.Fprintf(os.Stderr, "warn: env file %s: %v\n", path, err)
	}

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "version":
		err = runVersion(os.Args[2:])
	case "run":
		err = runRun(os.Args[2:])
	case "serve":
		err = runServe(os.Args[2:])
	case "config":
		err = fmt.Errorf("command %q is not implemented yet", os.Args[1])
	default:
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: trendradar <command> [flags]

commands:
  run       fetch, parse, summarize, render (M2/M3)
  serve     serve digests over HTTP (M5)
  config    inspect/validate config
  version   print version info; add --json for machine-readable output`)
}
