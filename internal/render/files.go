package render

import (
	"encoding/json"
	"path"
	"strconv"

	"github.com/morehao/trendradar/internal/digest"
)

// File is one rendered artifact (path is relative, use forward slashes).
type File struct {
	Path string
	Data []byte
}

// JSON serializes a digest as indented JSON.
func JSON(d digest.Digest) ([]byte, error) {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Build returns the markdown, json and index files for a digest. indexDigests
// is the set of digests to list on the index page (newest first by date).
func Build(d digest.Digest, indexDigests []digest.Digest, indexLimit int) ([]File, error) {
	md := Markdown(d)
	js, err := JSON(d)
	if err != nil {
		return nil, err
	}
	dir := path.Join("digests", d.Date)
	return []File{
		{Path: path.Join(dir, d.Date+".md"), Data: md},
		{Path: path.Join(dir, d.Date+".json"), Data: js},
		{Path: path.Join("digests", "index.md"), Data: Index(indexDigests, indexLimit)},
	}, nil
}

// CountFiles is a tiny helper for CLI output.
func CountFiles(files []File) string { return strconv.Itoa(len(files)) }
