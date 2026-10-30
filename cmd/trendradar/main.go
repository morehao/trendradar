package main

import (
	"fmt"
	"os"
)

func main() {
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
	case "serve", "config":
		err = fmt.Errorf("command %q is not implemented yet (lands in M5)", os.Args[1])
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
