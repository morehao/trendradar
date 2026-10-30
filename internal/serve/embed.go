package serve

import (
	"embed"
	"io/fs"
)

//go:embed web/*
var webFS embed.FS

// webSub returns the embedded web/ directory as an fs.FS rooted at it.
func webSub() fs.FS {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		return webFS
	}
	return sub
}
