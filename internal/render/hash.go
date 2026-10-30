// Package render turns a digest into markdown/json files and a stable content hash.
package render

import "github.com/morehao/trendradar/internal/digest"

// ContentHash is a stable sha256 over RFC 8785 JCS of the digest, excluding
// generatedAt, coreVersion, contentHash and contentHashAlgo.
func ContentHash(d digest.Digest) string { return digest.ContentHashJCS(d) }
