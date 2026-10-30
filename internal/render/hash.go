// Package render turns a digest into markdown/json files and a stable content hash.
package render

import "github.com/morehao/trendradar/internal/digest"

// ContentHash is a stable sha256 over RFC 8785 JCS of the digest, excluding
// generatedAt, coreVersion, contentHash and contentHashAlgo, so reruns produce
// the same value and Go/TS agree on the wire.
func ContentHash(d digest.Digest) string { return d.ContentHashJCS() }
