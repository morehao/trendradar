// Package render turns a digest into markdown/json files and a stable content hash.
package render

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/morehao/trendradar/internal/digest"
)

// ContentHash is a stable sha256 over the digest, excluding generatedAt,
// coreVersion and contentHash itself, so reruns produce the same value.
func ContentHash(d digest.Digest) string {
	type canon struct {
		Schema    int                 `json:"schema"`
		Date      string              `json:"date"`
		Period    string              `json:"period"`
		Languages []string            `json:"languages"`
		Count     int                 `json:"count"`
		Degraded  bool                `json:"degraded"`
		Repos     []digest.RepoDigest `json:"repos"`
	}
	b, _ := json.Marshal(canon{
		Schema:    d.Schema,
		Date:      d.Date,
		Period:    d.Period,
		Languages: d.Languages,
		Count:     d.Count,
		Degraded:  d.Degraded,
		Repos:     d.Repos,
	})
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
