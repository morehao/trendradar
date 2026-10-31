package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/morehao/trendradar/internal/config"
	"github.com/morehao/trendradar/internal/subscription"
)

// runSubscribe implements `trendradar subscribe <list|add|remove>`.
//
// Both run and subscribe resolve the subscriptions file with the same rule:
// explicit --file wins, else --config's releases.subscriptionsFile, else
// ./subscriptions.yaml, with relative paths anchored at the config directory.
func runSubscribe(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: trendradar subscribe <list|add|remove> [owner/repo...] [--config config.yaml | --file subscriptions.yaml]")
	}
	action := args[0]
	cfgPath, fileFlag, positional, err := splitSubscribeFlags(args[1:])
	if err != nil {
		return err
	}

	configured := fileFlag
	if configured == "" && cfgPath != "" {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			return err
		}
		configured = cfg.Releases.SubscriptionsFile
	}
	path, err := subscription.ResolveSubscriptionsPath(cfgPath, configured)
	if err != nil {
		return err
	}
	store, err := subscription.Load(path)
	if err != nil {
		return err
	}

	switch action {
	case "list":
		if len(positional) != 0 {
			return fmt.Errorf("usage: trendradar subscribe list [--config config.yaml | --file subscriptions.yaml]")
		}
		for _, repo := range store.List() {
			fmt.Println(repo)
		}
		if store.Len() == 0 {
			fmt.Fprintf(os.Stderr, "(no subscriptions in %s)\n", path)
		}
		return nil
	case "add":
		if len(positional) == 0 {
			return fmt.Errorf("usage: trendradar subscribe add owner/repo [owner/repo...]")
		}
		added, err := store.Add(positional...)
		if err != nil {
			return err
		}
		fmt.Printf("added %d subscription(s) to %s\n", added, path)
		return nil
	case "remove":
		if len(positional) != 1 {
			return fmt.Errorf("usage: trendradar subscribe remove owner/repo")
		}
		if err := store.Remove(positional[0]); err != nil {
			return err
		}
		fmt.Printf("removed %s from %s\n", positional[0], path)
		return nil
	default:
		return fmt.Errorf("unknown subscribe action %q (want list|add|remove)", action)
	}
}

// splitSubscribeFlags pulls --config/--file out of args wherever they appear,
// so `subscribe add repo --file x` and `add --file x repo` both work (the
// standard flag package stops parsing at the first positional argument).
func splitSubscribeFlags(args []string) (cfgPath, file string, positional []string, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--config" || a == "-config":
			if i+1 >= len(args) {
				return "", "", nil, fmt.Errorf("%s needs a value", a)
			}
			cfgPath = args[i+1]
			i++
		case strings.HasPrefix(a, "--config="):
			cfgPath = strings.TrimPrefix(a, "--config=")
		case a == "--file" || a == "-file":
			if i+1 >= len(args) {
				return "", "", nil, fmt.Errorf("%s needs a value", a)
			}
			file = args[i+1]
			i++
		case strings.HasPrefix(a, "--file="):
			file = strings.TrimPrefix(a, "--file=")
		default:
			positional = append(positional, a)
		}
	}
	return cfgPath, file, positional, nil
}
