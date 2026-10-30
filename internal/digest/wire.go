package digest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/gowebpki/jcs"
)

// ContentHashJCS computes sha256 over RFC 8785 JCS of the fixed basis object
// (schema,date,period,languages,count,degraded,repos), excluding
// generatedAt/coreVersion/contentHash/contentHashAlgo. Go and TS agree on the
// wire value.
func ContentHashJCS(d Digest) string {
	basis := struct {
		Schema    int          `json:"schema"`
		Date      string       `json:"date"`
		Period    string       `json:"period"`
		Languages []string     `json:"languages"`
		Count     int          `json:"count"`
		Degraded  bool         `json:"degraded"`
		Repos     []RepoDigest `json:"repos"`
	}{d.Schema, d.Date, d.Period, d.Languages, d.Count, d.Degraded, d.Repos}

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
