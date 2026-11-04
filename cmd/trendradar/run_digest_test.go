package main

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/morehao/trendradar/internal/app"
	"github.com/morehao/trendradar/internal/render"
)

func TestDigestArtifactCarriesBytesAndHash(t *testing.T) {
	body := []byte("---\ndate: 2026-11-04\n---\n\n# 2026-11-04 趋势摘要\n\n## all\n\n| 仓库 |\n")
	res := app.Result{
		DigestPath: "digests/daily/2026-11/2026-11-04.md",
		Files: []render.File{
			{Path: "digests/index.md", Data: []byte("# index\n")},
			{Path: "digests/daily/2026-11/2026-11-04.md", Data: body},
		},
	}
	got := digestArtifact(res, false)
	if got == nil {
		t.Fatal("digestArtifact returned nil for a rendered digest")
	}
	if got.Path != res.DigestPath {
		t.Fatalf("path = %q", got.Path)
	}
	if got.Bytes != len(body) {
		t.Fatalf("bytes = %d, want %d", got.Bytes, len(body))
	}
	if got.Text != string(body) {
		t.Fatal("text does not round-trip the artifact bytes")
	}
	sum := sha256.Sum256(body)
	if want := "sha256:" + hex.EncodeToString(sum[:]); got.SHA256 != want {
		t.Fatalf("sha256 = %q, want %q", got.SHA256, want)
	}

	if digestArtifact(res, true) != nil {
		t.Fatal("dry runs must not inline the digest")
	}
	if digestArtifact(app.Result{}, false) != nil {
		t.Fatal("a run that rendered nothing must not inline the digest")
	}
}
