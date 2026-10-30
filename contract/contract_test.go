package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var fixtures = []string{
	"digest-normal.json",
	"digest-degraded.json",
	"digest-empty-lang.json",
	"digest-missing-optional.json",
}

func readFile(t *testing.T, elems ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(elems...))
	if err != nil {
		t.Fatalf("read %v: %v", elems, err)
	}
	return b
}

func compileDigestSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	add := func(f string) {
		doc, err := jsonschema.UnmarshalJSON(bytesReader(readFile(t, "schema", f)))
		if err != nil {
			t.Fatalf("unmarshal schema %s: %v", f, err)
		}
		if err := c.AddResource("https://github.com/morehao/trendradar/contract/schema/"+f, doc); err != nil {
			t.Fatalf("add resource %s: %v", f, err)
		}
	}
	add("model.v2.json")
	add("digest.v2.json")
	s, err := c.Compile("https://github.com/morehao/trendradar/contract/schema/digest.v2.json")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return s
}

func TestFixturesValidate(t *testing.T) {
	s := compileDigestSchema(t)
	for _, f := range fixtures {
		raw := readFile(t, "fixtures", f)
		doc, err := jsonschema.UnmarshalJSON(bytesReader(raw))
		if err != nil {
			t.Fatalf("%s: unmarshal: %v", f, err)
		}
		if err := s.Validate(doc); err != nil {
			t.Fatalf("%s: invalid against v2 schema: %v", f, err)
		}
	}
}

// hashBasis is the exact object hashed by contentHash (generatedAt/coreVersion/
// contentHash/contentHashAlgo excluded). Keep in sync with convert-fixtures.mjs.
type hashBasis struct {
	Schema    int          `json:"schema"`
	Date      string       `json:"date"`
	Period    string       `json:"period"`
	Languages []string     `json:"languages"`
	Count     int          `json:"count"`
	Degraded  bool         `json:"degraded"`
	Repos     []RepoDigest `json:"repos"`
}

func jcsContentHash(t *testing.T, d Digest) string {
	t.Helper()
	basis := hashBasis{d.Schema, d.Date, d.Period, d.Languages, d.Count, d.Degraded, d.Repos}
	raw, err := json.Marshal(basis)
	if err != nil {
		t.Fatalf("marshal basis: %v", err)
	}
	canon, err := jcs.Transform(raw)
	if err != nil {
		t.Fatalf("jcs: %v", err)
	}
	sum := sha256.Sum256(canon)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestContentHashMatchesFixture(t *testing.T) {
	for _, f := range fixtures {
		var d Digest
		if err := json.Unmarshal(readFile(t, "fixtures", f), &d); err != nil {
			t.Fatalf("%s: unmarshal: %v", f, err)
		}
		if got := jcsContentHash(t, d); got != d.ContentHash {
			t.Errorf("%s: jcs hash = %s, want %s", f, got, d.ContentHash)
		}
	}
}

func TestErrorCatalogComplete(t *testing.T) {
	if len(ErrorCatalog) != 17 {
		t.Fatalf("ErrorCatalog size = %d, want 17", len(ErrorCatalog))
	}
	if !ErrorCodeFETCH_RATE_LIMITED.Retriable() {
		t.Error("FETCH_RATE_LIMITED should be retriable")
	}
	if ErrorCodeREPO_DIRTY.Retriable() {
		t.Error("REPO_DIRTY should not be retriable")
	}
	if ErrorCode("NOPE").Valid() {
		t.Error("unknown code should be invalid")
	}
}
