package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/morehao/trendradar/internal/ipc"
)

// runVersion implements `trendradar version [--json]`.
func runVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	info := ipc.Current()
	if *asJSON {
		b, err := json.Marshal(info)
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}

	fmt.Printf("trendradar %s (commit %s, ipcSchema %d, digestSchema %d)\n",
		info.Version, info.Commit, info.IPCSchema, info.DigestSchema)
	fmt.Printf("supported providers: %v\n", info.SupportedProviders)
	return nil
}
