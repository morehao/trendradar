package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	contract "github.com/morehao/trendradar/contract"
	"github.com/morehao/trendradar/internal/config"
	"github.com/morehao/trendradar/internal/ipc"
	"github.com/morehao/trendradar/internal/releasedoc"
	"github.com/morehao/trendradar/internal/releasewatch"
	"github.com/morehao/trendradar/internal/render"
	"github.com/morehao/trendradar/internal/subscription"
)

// runSubscribe implements `trendradar subscribe <list|add|remove|releases>`.
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
	_, err := runSubscribeOutput(args)
	return err
}

// subscribeOutput is what one subscribe command produced. list fills List,
// releases fills Doc; add/remove fill neither (they only queue an intent).
type subscribeOutput struct {
	List *contract.SubscriptionList
	Doc  *contract.SubscriptionDoc
}

// runSubscribeOutput is the shared implementation behind the CLI and the IPC
// entry point, so both return the same contract types.
func runSubscribeOutput(args []string) (subscribeOutput, error) {
	if len(args) == 0 {
		return subscribeOutput{}, fmt.Errorf("usage: trendradar subscribe <list|add|remove|releases> [owner/repo] --config config.yaml [--file subscriptions.yaml] [--volume N]")
	}
	action := args[0]
	cfgPath, fileFlag, volumeRaw, positional, err := splitSubscribeFlags(args[1:])
	if err != nil {
		return subscribeOutput{}, err
	}

	// --file is a read-only override: only list honours it.
	if fileFlag != "" {
		if action != "list" {
			return subscribeOutput{}, ipc.NewError(ipc.CodeConfigInvalid,
				"subscribe %s does not accept --file; subscriptions are versioned in the data repo (--file is read-only, for `list` only)", action)
		}
		if err := listFromFile(cfgPath, fileFlag, positional); err != nil {
			return subscribeOutput{}, err
		}
		return subscribeOutput{}, nil
	}

	// Repo-versioned path: requires --config so run and subscribe resolve the
	// same repo/output dir. Guessing ./subscriptions.yaml would write outside the
	// caller's data repo and depend on an unpredictable working directory.
	if cfgPath == "" {
		return subscribeOutput{}, ipc.NewError(ipc.CodeConfigInvalid,
			"subscribe requires --config so run and subscribe resolve the same subscriptions file")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return subscribeOutput{}, err
	}

	repoPath := cfg.Repo.Path
	outputDir := cfg.Output.Dir
	file := cfg.Releases.SubscriptionsFile
	today := time.Now().Format("2006-01-02")
	branch := subscription.RunBranchNameGrouped(cfg.Output.BranchPrefix, cfg.Output.BranchGrouping, cfg.Releases.Period, today)

	if action == "releases" {
		return subscribeReleases(cfg, positional, volumeRaw)
	}

	base, err := subscription.ReadRepoWithFallback(repoPath, outputDir, file, branch, cfg.Repo.BaseBranch)
	if err != nil {
		return subscribeOutput{}, err
	}
	pend, hasPending, err := subscription.ReadPending(repoPath)
	if err != nil {
		return subscribeOutput{}, err
	}
	// The user-visible "current" list is the branch file unioned with the
	// not-yet-committed pending queue.
	current, _ := subscription.MergeUnique(base.List(), pend)

	switch action {
	case "list":
		if len(positional) != 0 {
			return subscribeOutput{}, fmt.Errorf("usage: trendradar subscribe list --config config.yaml [--file subscriptions.yaml]")
		}
		// items[] is the additive detail the panel's table needs. It is derived
		// from the local cursor plus the per-repo document index, so list never
		// reads a document body (A2).
		items := releasewatch.Items(
			releasewatch.StatePath(repoPath),
			repoPath,
			render.ReleaseRoot(outputDir, cfg.Releases.OutputDir),
			current,
		)
		list := &contract.SubscriptionList{Repos: current, Items: items}
		for _, repo := range current {
			fmt.Println(repo)
		}
		if len(current) == 0 {
			fmt.Fprintf(os.Stderr, "(no subscriptions yet; branch %s has none and no pending changes)\n", branch)
		} else if hasPending {
			fmt.Fprintf(os.Stderr, "(%d change(s) pending; they will be committed by the next `trendradar run`)\n", len(pend))
		}
		return subscribeOutput{List: list}, nil
	case "add":
		if len(positional) == 0 {
			return subscribeOutput{}, fmt.Errorf("usage: trendradar subscribe add owner/repo [owner/repo...] --config config.yaml")
		}
		// Validate inputs through the Store so a bad entry never reaches pending.
		next, err := validateAndMerge(current, positional)
		if err != nil {
			return subscribeOutput{}, err
		}
		_, added := subscription.MergeUnique(current, positional)
		if added == 0 {
			fmt.Printf("all %d subscription(s) already present; nothing to do\n", len(positional))
			return subscribeOutput{}, nil
		}
		if err := subscription.WritePending(repoPath, next); err != nil {
			return subscribeOutput{}, err
		}
		fmt.Printf("queued %d new subscription(s); they will be committed by the next `trendradar run` (branch %s)\n", added, branch)
		return subscribeOutput{}, nil
	case "remove":
		if len(positional) != 1 {
			return subscribeOutput{}, fmt.Errorf("usage: trendradar subscribe remove owner/repo --config config.yaml")
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
			return subscribeOutput{}, ipc.NewError(ipc.CodeConfigInvalid, "repo %q is not subscribed", target)
		}
		if err := subscription.WritePending(repoPath, next); err != nil {
			return subscribeOutput{}, err
		}
		fmt.Printf("queued removal of %s; it will be committed by the next `trendradar run` (branch %s)\n", target, branch)
		return subscribeOutput{}, nil
	default:
		return subscribeOutput{}, fmt.Errorf("unknown subscribe action %q (want list|add|remove|releases)", action)
	}
}

// subscribeReleases reads one volume of a repo's release document. It is the
// read-only counterpart of the weekly writer: the panel renders the returned
// markdown verbatim, so the bytes here are the bytes in the data repo (A7).
func subscribeReleases(cfg *config.Config, positional []string, volumeRaw string) (subscribeOutput, error) {
	if len(positional) != 1 {
		return subscribeOutput{}, fmt.Errorf("usage: trendradar subscribe releases --repo owner/repo [--volume N] --config config.yaml")
	}
	repo := strings.TrimSpace(positional[0])
	volume, err := releasedoc.ParseVolume(volumeRaw)
	if err != nil {
		return subscribeOutput{}, ipc.NewError(ipc.CodeConfigInvalid, "%s", err.Error())
	}
	doc, err := releasedoc.Read(
		cfg.Repo.Path,
		render.ReleaseRoot(cfg.Output.Dir, cfg.Releases.OutputDir),
		repo,
		volume,
	)
	switch {
	case err == nil:
	case err == releasedoc.ErrNoDoc:
		return subscribeOutput{}, ipc.NewError(ipc.CodeConfigInvalid, "repo %q has no release document yet", repo)
	case err == releasedoc.ErrVolumeOutOfRange:
		return subscribeOutput{}, ipc.NewError(ipc.CodeConfigInvalid, "volume %d does not exist for %q", volume, repo)
	default:
		return subscribeOutput{}, err
	}
	if volumeRaw == "" {
		fmt.Fprintf(os.Stderr, "(volume %d of %d)\n", doc.Volume, len(doc.Volumes))
	}
	fmt.Print(doc.Markdown)
	return subscribeOutput{Doc: &doc}, nil
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

// splitSubscribeFlags pulls --config/--file/--volume out of args wherever they
// appear, so `subscribe add repo --file x` and `add --file x repo` both work (the
// standard flag package stops parsing at the first positional argument).
func splitSubscribeFlags(args []string) (cfgPath, file, volume string, positional []string, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--config" || a == "-config":
			if i+1 >= len(args) {
				return "", "", "", nil, fmt.Errorf("%s needs a value", a)
			}
			cfgPath = args[i+1]
			i++
		case strings.HasPrefix(a, "--config="):
			cfgPath = strings.TrimPrefix(a, "--config=")
		case a == "--file" || a == "-file":
			if i+1 >= len(args) {
				return "", "", "", nil, fmt.Errorf("%s needs a value", a)
			}
			file = args[i+1]
			i++
		case strings.HasPrefix(a, "--file="):
			file = strings.TrimPrefix(a, "--file=")
		case a == "--volume" || a == "-volume":
			if i+1 >= len(args) {
				return "", "", "", nil, fmt.Errorf("%s needs a value", a)
			}
			volume = args[i+1]
			i++
		case strings.HasPrefix(a, "--volume="):
			volume = strings.TrimPrefix(a, "--volume=")
		default:
			positional = append(positional, a)
		}
	}
	return cfgPath, file, volume, positional, nil
}
