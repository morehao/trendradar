package render

import (
	"encoding/json"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/morehao/trendradar/internal/digest"
)

// File is one rendered artifact (path is relative, use forward slashes).
type File struct {
	Path string
	Data []byte
}

// JSON serializes a digest as indented v2 JSON (schema=2, contentHashAlgo).
func JSON(d digest.Digest) ([]byte, error) {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Build returns the markdown, json and index files for a digest. indexDigests
// is the set of digests to list on the index page (newest first by date).
//
// outDir is the digest root inside the repository (config.output.dir). It MUST
// match what the caller stages with `git add`, otherwise a run writes files
// under one directory and stages another. Empty means "digests". Every returned
// path is relative to the repository root.
func Build(d digest.Digest, indexDigests []digest.Digest, indexLimit int, outDir string) ([]File, error) {
	root := OutputRoot(outDir)
	md := Markdown(d)
	js, err := JSON(d)
	if err != nil {
		return nil, err
	}
	dir := path.Join(root, d.Date)
	return []File{
		{Path: path.Join(dir, d.Date+".md"), Data: md},
		{Path: path.Join(dir, d.Date+".json"), Data: js},
		{Path: path.Join(root, "index.md"), Data: Index(indexDigests, indexLimit, root)},
	}, nil
}

// OutputRoot normalizes a configured output dir into a safe repository-relative
// digest root. Absolute paths and parent traversal fall back to "digests" so a
// bad config can never make the core write outside the repo. It is the single
// source of truth shared by rendering and staging.
func OutputRoot(outDir string) string {
	root := strings.TrimSpace(path.Clean(strings.ReplaceAll(outDir, "\\", "/")))
	if root == "" || root == "." || path.IsAbs(root) || root == ".." || strings.HasPrefix(root, "../") {
		return "digests"
	}
	return root
}

// CountFiles is a tiny helper for CLI output.
func CountFiles(files []File) string { return strconv.Itoa(len(files)) }

var (
	// generatedAt is the only field whose value changes on every run even when
	// the digest content is identical. It is metadata about the run, not the
	// digest, so it is excluded when deciding "did anything change?".
	genAtMarkdown = regexp.MustCompile(`(?m)^generatedAt:.*\n?`)
	genAtJSON     = regexp.MustCompile(`"generatedAt":\s*("[^"]*"|null)`)
)

// Comparable returns data with volatile per-run fields removed, so two renders
// of the same digest compare equal. It is used for idempotency: a branch whose
// files already normalize to the same bytes must not gain another commit.
func Comparable(filePath string, data []byte) []byte {
	if strings.HasSuffix(strings.ToLower(filePath), ".json") {
		return genAtJSON.ReplaceAll(data, []byte(`"generatedAt":null`))
	}
	return genAtMarkdown.ReplaceAll(data, nil)
}
