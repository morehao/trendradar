package digest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/gowebpki/jcs"
	contract "github.com/morehao/trendradar/contract"
)

// Contract projects the internal domain digest onto the shared wire type.
func (d Digest) Contract() contract.Digest {
	return d.ContractWithAlgo("jcs-sha256-v1")
}

// ContractWithAlgo projects onto the wire type with an explicit contentHashAlgo
// (used for normalizing historical v1 files as legacy-struct-marshal-v1).
func (d Digest) ContractWithAlgo(algo string) contract.Digest {
	repos := make([]contract.RepoDigest, 0, len(d.Repos))
	for _, r := range d.Repos {
		repos = append(repos, r.contract())
	}
	var core *string
	if d.CoreVersion != "" {
		c := d.CoreVersion
		core = &c
	}
	var gen *time.Time
	if !d.GeneratedAt.IsZero() {
		g := d.GeneratedAt
		gen = &g
	}
	return contract.Digest{
		Schema:          2,
		Date:            d.Date,
		Period:          d.Period,
		Languages:       d.Languages,
		Count:           d.Count,
		Degraded:        d.Degraded,
		ContentHash:     d.ContentHash,
		ContentHashAlgo: algo,
		CoreVersion:     core,
		GeneratedAt:     gen,
		Repos:           repos,
	}
}

func (r RepoDigest) contract() contract.RepoDigest {
	var desc *string
	if r.Description != "" {
		s := r.Description
		desc = &s
	}
	var forks *int
	if r.Forks != 0 {
		f := r.Forks
		forks = &f
	}
	return contract.RepoDigest{
		RepoSnapshot: contract.RepoSnapshot{
			FullName:      r.FullName,
			URL:           r.URL,
			Description:   desc,
			Language:      r.Language,
			Stars:         r.Stars,
			StarsInPeriod: r.StarsInPeriod,
			Forks:         forks,
			Rank:          r.Rank,
			Source:        string(r.Source),
		},
		SummaryZh:     r.SummaryZh,
		SummarySource: r.SummarySource,
	}
}

// ContentHashJCS computes sha256 over the RFC 8785 JCS canonicalization of the
// contract basis object (schema,date,period,languages,count,degraded,repos),
// excluding generatedAt/coreVersion/contentHash/contentHashAlgo.
func (d Digest) ContentHashJCS() string {
	cd := d.Contract()
	basis := struct {
		Schema    int                   `json:"schema"`
		Date      string                `json:"date"`
		Period    string                `json:"period"`
		Languages []string              `json:"languages"`
		Count     int                   `json:"count"`
		Degraded  bool                  `json:"degraded"`
		Repos     []contract.RepoDigest `json:"repos"`
	}{cd.Schema, cd.Date, cd.Period, cd.Languages, cd.Count, cd.Degraded, cd.Repos}

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
