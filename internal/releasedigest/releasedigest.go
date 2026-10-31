// Package releasedigest holds the pure data types for the release-subscription
// digest. It deliberately imports no other internal package so render and
// releasewatch can both depend on it without a cycle.
package releasedigest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"

	"github.com/gowebpki/jcs"
)

// Schema is the internal release-document version (distinct from the trending
// contract digest schema, which is 2).
const Schema = 1

// ContentHashAlgo is recorded in the document so a future algorithm change is
// distinguishable from the current one.
const ContentHashAlgo = "jcs-sha256-v1"

// ReleaseItem is one release or tag entry.
type ReleaseItem struct {
	ID            int64      `json:"id"`
	Tag           string     `json:"tag"`
	Name          string     `json:"name"`
	Kind          string     `json:"kind"` // release | tag
	PublishedAt   *time.Time `json:"publishedAt,omitempty"`
	URL           string     `json:"url"`
	Prerelease    bool       `json:"prerelease"`
	Draft         bool       `json:"draft"`
	BodyExcerpt   string     `json:"bodyExcerpt,omitempty"`
	SummaryZh     string     `json:"summaryZh"`
	SummarySource string     `json:"summarySource"` // model | original
}

// RepoReleases groups the new items of one repository.
type RepoReleases struct {
	FullName  string        `json:"fullName"`
	URL       string        `json:"url"`
	LatestTag string        `json:"latestTag"`
	NewCount  int           `json:"newCount"`
	Releases  []ReleaseItem `json:"releases"`
}

// ReleaseDigest is one rendered period document.
type ReleaseDigest struct {
	Schema          int            `json:"schema"`
	Date            string         `json:"date"`
	Period          string         `json:"period"`
	ContentHash     string         `json:"contentHash"`
	ContentHashAlgo string         `json:"contentHashAlgo"`
	CoreVersion     string         `json:"coreVersion,omitempty"`
	GeneratedAt     *time.Time     `json:"generatedAt,omitempty"`
	Count           int            `json:"count"`
	Degraded        bool           `json:"degraded"`
	Repos           []RepoReleases `json:"repos"`
}

// Key identifies an item uniquely within a repo: id when known, else tag.
func (it ReleaseItem) Key() string {
	if it.ID != 0 {
		return it.Tag + "#" + strconv.FormatInt(it.ID, 10)
	}
	return it.Tag
}

// CountOf recomputes Count from the per-repo groups.
func (d *ReleaseDigest) CountOf() int {
	n := 0
	for _, r := range d.Repos {
		n += len(r.Releases)
	}
	return n
}

// DegradedOf reports whether any item fell back to the original notes.
func (d *ReleaseDigest) DegradedOf() bool {
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
// generatedAt/coreVersion/contentHash/contentHashAlgo (same approach as the
// trending digest, but a release-specific basis).
func ContentHash(d ReleaseDigest) string {
	basis := struct {
		Schema   int            `json:"schema"`
		Date     string         `json:"date"`
		Period   string         `json:"period"`
		Count    int            `json:"count"`
		Degraded bool           `json:"degraded"`
		Repos    []RepoReleases `json:"repos"`
	}{d.Schema, d.Date, d.Period, d.CountOf(), d.DegradedOf(), d.Repos}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(basis); err != nil {
		return ""
	}
	canon, err := jcs.Transform(bytes.TrimRight(buf.Bytes(), "\n"))
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(canon)
	return "sha256:" + hex.EncodeToString(sum[:])
}
