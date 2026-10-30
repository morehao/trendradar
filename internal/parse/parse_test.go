package parse

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/morehao/trendradar/internal/digest"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestGoldenAll(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "trending-all.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	repos, rep, err := ParseAll(map[string][]byte{"all": body})
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	if len(repos) == 0 {
		t.Fatal("parsed zero repos")
	}
	// row count must follow the fixture, not a hard-coded number
	if want := strings.Count(string(body), `class="Box-row"`); len(repos) != want {
		t.Fatalf("repos = %d, want %d (fixture rows)", len(repos), want)
	}
	if rep.Total == 0 {
		t.Fatal("report.Total = 0")
	}

	goldenPath := filepath.Join("testdata", "golden", "all.json")
	if *update {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		data, _ := json.MarshalIndent(repos, "", "  ")
		data = append(data, '\n')
		if err := os.WriteFile(goldenPath, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	var want []digest.RepoSnapshot
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("unmarshal golden: %v", err)
	}
	if !reflect.DeepEqual(repos, want) {
		got, _ := json.MarshalIndent(repos, "", "  ")
		t.Fatalf("golden mismatch\ngot:\n%s", got)
	}
}

func TestNoLoginAnchors(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "trending-all.html"))
	if err != nil {
		t.Fatal(err)
	}
	repos, _, err := ParseAll(map[string][]byte{"all": body})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range repos {
		if strings.Contains(r.URL, "login") || strings.Contains(r.FullName, "login") {
			t.Fatalf("login anchor leaked into repo: %+v", r)
		}
		if !strings.HasPrefix(r.URL, "https://github.com/") {
			t.Fatalf("bad url: %s", r.URL)
		}
	}
}

func TestMergeDedupKeepsBestRank(t *testing.T) {
	in := [][]digest.RepoSnapshot{
		{
			{FullName: "a/one", Rank: 3, Language: "go"},
			{FullName: "a/two", Rank: 1, Language: "go"},
		},
		{
			{FullName: "a/one", Rank: 1, Language: "go"}, // same repo, better rank
			{FullName: "a/three", Rank: 2, Language: "rust"},
		},
	}
	got := Merge(in)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i, r := range got {
		if r.Rank != i+1 {
			t.Fatalf("rank[%d] = %d, want %d", i, r.Rank, i+1)
		}
	}
	// the kept a/one must be the rank-1 occurrence, so a/one is overall rank 1
	if got[0].FullName != "a/one" {
		t.Fatalf("first = %s, want a/one", got[0].FullName)
	}
}

func TestParseEmptyHTMLYieldsZero(t *testing.T) {
	repos, rep, err := ParseAll(map[string][]byte{"all": []byte("<html><body>nothing</body></html>")})
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	if len(repos) != 0 || rep.Total != 0 {
		t.Fatalf("repos=%d total=%d, want 0/0", len(repos), rep.Total)
	}
}
