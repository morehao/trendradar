// Command contracthash writes contract/CONTRACT.sha256 deterministically.
// Algorithm: sha256 each file under schema/ and fixtures/ (sorted by
// forward-slash relative path), then sha256 the concatenated "<hash>  <path>"
// lines and record that as the aggregate.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	root := flag.String("contract", "contract", "path to the contract directory")
	flag.Parse()

	subdirs := []string{"schema", "fixtures"}
	var files []string
	for _, d := range subdirs {
		base := filepath.Join(*root, d)
		err := filepath.WalkDir(base, func(path string, de fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if de.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(*root, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			fatal(err)
		}
	}
	sort.Strings(files)

	agg := sha256.New()
	var lines []string
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(*root, filepath.FromSlash(rel)))
		if err != nil {
			fatal(err)
		}
		sum := sha256.Sum256(data)
		line := fmt.Sprintf("%s  %s", hex.EncodeToString(sum[:]), rel)
		lines = append(lines, line)
		_, _ = io.WriteString(agg, line+"\n")
	}
	aggregate := hex.EncodeToString(agg.Sum(nil))

	var b strings.Builder
	b.WriteString("# trendradar contract hash (v1)\n")
	b.WriteString("# algorithm: sha256 per file (sorted), then sha256 of the concatenated lines\n")
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	fmt.Fprintf(&b, "aggregate: %s\n", aggregate)

	out := filepath.Join(*root, "CONTRACT.sha256")
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %s (aggregate %s)\n", out, aggregate)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "contracthash:", err)
	os.Exit(1)
}
