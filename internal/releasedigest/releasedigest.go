// Package releasedigest adapts the contract's release types for the core.
//
// The wire shapes live in the contract package (single source of truth: schema/
// releases.v2.json → contract.gen.go / gen/ts). This package only adds the
// behaviours the core needs on top of those types (content hash, derived
// counts), mirroring how internal/digest aliases contract types.
package releasedigest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"

	"github.com/gowebpki/jcs"

	contract "github.com/morehao/trendradar/contract"
)

// Schema is the release-document version carried in the document itself. It is
// independent of both the contract package version and ipcSchema/digestSchema.
const Schema = 1

// ContentHashAlgo is recorded in the document so a future algorithm change is
// distinguishable from the current one.
const ContentHashAlgo = "jcs-sha256-v1"

// Aliases to the contract wire types. No duplicate struct definitions exist:
// the shape is defined once, in contract/schema/releases.v2.json.
type (
	ReleaseDigest = contract.ReleaseDigest
	RepoReleases  = contract.RepoReleases
	ReleaseItem   = contract.ReleaseItem
)

// Key identifies an item uniquely within a repo: id when known, else tag.
func ItemKey(it ReleaseItem) string {
	if it.ID != 0 {
		return it.Tag + "#" + strconv.FormatInt(it.ID, 10)
	}
	return it.Tag
}

// CountOf recomputes Count from the per-repo groups.
func CountOf(d ReleaseDigest) int {
	n := 0
	for _, r := range d.Repos {
		n += len(r.Releases)
	}
	return n
}

// DegradedOf reports whether any item fell back to the original notes.
func DegradedOf(d ReleaseDigest) bool {
	for _, r := range d.Repos {
		for _, it := range r.Releases {
			if it.SummarySource == "original" {
				return true
			}
		}
	}
	return false
}

// ContentHash computes sha256 over RFC 8785 JCS of the fixed basis, excluding
// generatedAt/coreVersion/contentHash/contentHashAlgo. The basis is
// release-specific (no languages; repos are release groups), so this cannot
// reuse digest.ContentHashJCS. The contract tests recompute the same basis in
// test/contract.test.mjs — keep them in sync.
func ContentHash(d ReleaseDigest) string {
	basis := struct {
		Schema   int            `json:"schema"`
		Date     string         `json:"date"`
		Period   string         `json:"period"`
		Count    int            `json:"count"`
		Degraded bool           `json:"degraded"`
		Repos    []RepoReleases `json:"repos"`
	}{d.Schema, d.Date, d.Period, CountOf(d), DegradedOf(d), d.Repos}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(basis); err != nil {
		return ""
	}
	canon, err := jcsTransform(bytes.TrimRight(buf.Bytes(), "\n"))
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(canon)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// jcsTransform is a thin wrapper so the JCS dependency stays in one place.
func jcsTransform(b []byte) ([]byte, error) { return jcs.Transform(b) }
