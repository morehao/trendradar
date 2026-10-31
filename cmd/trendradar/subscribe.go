package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/morehao/trendradar/internal/config"
	"github.com/morehao/trendradar/internal/ipc"
	"github.com/morehao/trendradar/internal/subscription"
)

// runSubscribe implements `trendradar subscribe <list|add|remove>`.
//
// The subscription list is now a versioned file inside the data repo
// (<repo>/<output.dir>/subscriptions.yaml). subscribe must NOT write the working
// tree: run's IsClean check treats any tracked-file change as REPO_DIRTY from
// the second cycle on. Instead add/remove write a pending queue under .git/, and
// the next run lands it on the date branch with the artifacts (design 2026-10-31
// §算法, §为什么不能用直接写工作区).
//
// --file keeps a read-only escape hatch for troubleshooting: `list` reads that
// file, but `add`/`remove` return CONFIG_INVALID because a one-off file is not
// the versioned source of truth.
func runSubscribe(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: trendradar subscribe <list|add|remove> [owner/repo...] --config config.yaml [--file subscriptions.yaml]")
	}
	action := args[0]
	cfgPath, fileFlag, positional, err := splitSubscribeFlags(args[1:])
	if err != nil {
		return err
	}

	// --file is a read-only override: only list honours it.
	if fileFlag != "" {
		if action != "list" {
			return ipc.NewError(ipc.CodeConfigInvalid,
				"subscribe %s does not accept --file; subscriptions are versioned in the data repo (--file is read-only, for `list` only)", action)
		}
		return listFromFile(cfgPath, fileFlag, positional)
	}

	// Repo-versioned path: requires --config so run and subscribe resolve the
	// same repo/output dir. Guessing ./subscriptions.yaml would write outside the
	// caller's data repo and depend on an unpredictable working directory.
	if cfgPath == "" {
		return ipc.NewError(ipc.CodeConfigInvalid,
			"subscribe requires --config so run and subscribe resolve the same subscriptions file")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	repoPath := cfg.Repo.Path
	outputDir := cfg.Output.Dir
	file := cfg.Releases.SubscriptionsFile
	today := time.Now().Format("2006-01-02")
	branch := subscription.RunBranchName(cfg.Output.BranchPrefix, cfg.Releases.Period, today)

	base, err := subscription.ReadRepoOnTargetBranch(repoPath, outputDir, file, branch)
	if err != nil {
		return err
	}
	pend, hasPending, err := subscription.ReadPending(repoPath)
	if err != nil {
		return err
	}
	// The user-visible "current" list is the branch file unioned with the
	// not-yet-committed pending queue.
	current, _ := subscription.MergeUnique(base.List(), pend)

	switch action {
	case "list":
		if len(positional) != 0 {
			return fmt.Errorf("usage: trendradar subscribe list --config config.yaml [--file subscriptions.yaml]")
		}
		for _, repo := range current {
			fmt.Println(repo)
		}
		if len(current) == 0 {
			fmt.Fprintf(os.Stderr, "(no subscriptions yet; branch %s has none and no pending changes)\n", branch)
		} else if hasPending {
			fmt.Fprintf(os.Stderr, "(%d change(s) pending; they will be committed by the next `trendradar run`)\n", len(pend))
		}
		return nil
	case "add":
		if len(positional) == 0 {
			return fmt.Errorf("usage: trendradar subscribe add owner/repo [owner/repo...] --config config.yaml")
		}
		// Validate inputs through the Store so a bad entry never reaches pending.
		next, err := validateAndMerge(current, positional)
		if err != nil {
			return err
		}
		_, added := subscription.MergeUnique(current, positional)
		if added == 0 {
			fmt.Printf("all %d subscription(s) already present; nothing to do\n", len(positional))
			return nil
		}
		if err := subscription.WritePending(repoPath, next); err != nil {
			return err
		}
		fmt.Printf("queued %d new subscription(s); they will be committed by the next `trendradar run` (branch %s)\n", added, branch)
		return nil
	case "remove":
		if len(positional) != 1 {
			return fmt.Errorf("usage: trendradar subscribe remove owner/repo --config config.yaml")
		}
		target := strings.TrimSpace(positional[0])
		next := make([]string, 0, len(current))
		found := false
		for _, r := range current {
			if r == target {
				found = true
				continue
			}
			next = append(next, r)
		}
		if !found {
			return ipc.NewError(ipc.CodeConfigInvalid, "repo %q is not subscribed", target)
		}
		if err := subscription.WritePending(repoPath, next); err != nil {
			return err
		}
		fmt.Printf("queued removal of %s; it will be committed by the next `trendradar run` (branch %s)\n", target, branch)
		return nil
	default:
		return fmt.Errorf("unknown subscribe action %q (want list|add|remove)", action)
	}
}

// validateAndMerge validates each new repo and returns current ∪ new. It shares
// the Store's owner/repo regex through subscription's validation by round-
// tripping the merged list, so an invalid entry fails before WritePending.
func validateAndMerge(current, add []string) ([]string, error) {
	merged, _ := subscription.MergeUnique(current, add)
	// Re-validate via a throwaway store bound to a path we never write.
	_, err := subscription.ValidateList(merged)
	if err != nil {
		return nil, err
	}
	return merged, nil
}

// listFromFile implements the read-only --file path used for troubleshooting.
func listFromFile(cfgPath, file string, positional []string) error {
	if len(positional) != 0 {
		return fmt.Errorf("usage: trendradar subscribe list [--config config.yaml | --file subscriptions.yaml]")
	}
	path, err := subscription.ResolveSubscriptionsPath(cfgPath, file)
	if err != nil {
		return err
	}
	store, err := subscription.Load(path)
	if err != nil {
		return err
	}
	for _, repo := range store.List() {
		fmt.Println(repo)
	}
	if store.Len() == 0 {
		fmt.Fprintf(os.Stderr, "(no subscriptions in %s)\n", path)
	}
	return nil
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
