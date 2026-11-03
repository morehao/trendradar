package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newStarsClient points a Client at a test server for both HTML and API hosts.
func newStarsClient(t *testing.T, srvURL string) *Client {
	t.Helper()
	return NewClient(Options{BaseURL: srvURL, APIBaseURL: srvURL, RetryInterval: time.Millisecond, MaxRetry: 1})
}

// TestSplitFullName exercises the owner/name splitter.
func TestSplitFullName(t *testing.T) {
	cases := []struct {
		in   string
		o, n string
		ok   bool
	}{
		{"golang/go", "golang", "go", true},
		{" owner/name ", "owner", "name", true},
		{"bad", "", "", false},
		{"a/b/c", "", "", false},
		{"/name", "", "", false},
		{"owner/", "", "", false},
	}
	for _, c := range cases {
		o, n, ok := splitFullName(c.in)
		if o != c.o || n != c.n || ok != c.ok {
			t.Fatalf("splitFullName(%q) = (%q,%q,%v), want (%q,%q,%v)", c.in, o, n, ok, c.o, c.n, c.ok)
		}
	}
}

// TestFetchStarsGraphQLAliases verifies the batched alias query shape and parsing.
func TestFetchStarsGraphQLAliases(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/graphql" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var req graphqlStarsRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("bad request body: %v", err)
		}
		gotQuery = req.Query
		// Echo back a node per alias found in the query.
		resp := map[string]any{"data": map[string]any{}}
		data := resp["data"].(map[string]any)
		data["r0"] = map[string]any{"stargazerCount": 1000, "forkCount": 50}
		data["r1"] = map[string]any{"stargazerCount": 2000, "forkCount": 60}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newStarsClient(t, srv.URL)
	out, err := c.FetchStars(context.Background(), []string{"golang/go", "python/cpython"})
	if err != nil {
		t.Fatalf("FetchStars: %v", err)
	}
	if !strings.Contains(gotQuery, `r0: repository(owner: "golang", name: "go")`) {
		t.Fatalf("query missing r0 alias: %s", gotQuery)
	}
	if !strings.Contains(gotQuery, `r1: repository(owner: "python", name: "cpython")`) {
		t.Fatalf("query missing r1 alias: %s", gotQuery)
	}
	if strings.Contains(gotQuery, "star") && strings.Contains(gotQuery, "stargazerCount") == false {
		t.Fatal("query should request stargazerCount")
	}
	if out["golang/go"].Stars != 1000 || out["python/cpython"].Stars != 2000 {
		t.Fatalf("parsed stars wrong: %+v", out)
	}
	if out["golang/go"].Forks != 50 {
		t.Fatalf("parsed forks wrong: %+v", out["golang/go"])
	}
}

// TestFetchStarsBatches verifies requests are split at graphqlBatchSize.
func TestFetchStarsBatches(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		// Respond with a null-free data map keyed by whatever aliases appear.
		var req graphqlStarsRequest
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		data := map[string]any{}
		for i := 0; i < countAliases(req.Query); i++ {
			data[aliasFor(i)] = map[string]any{"stargazerCount": i, "forkCount": 0}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()

	c := newStarsClient(t, srv.URL)
	repos := make([]string, 0, 250)
	for i := 0; i < 250; i++ {
		repos = append(repos, fmt.Sprintf("owner%d/repo%d", i, i))
	}
	if _, err := c.FetchStars(context.Background(), repos); err != nil {
		t.Fatalf("FetchStars: %v", err)
	}
	if want := 3; requests != want { // 100 + 100 + 50
		t.Fatalf("requests = %d, want %d", requests, want)
	}
}

// TestFetchStarsNullRepo skips repos GitHub reports as null without error.
func TestFetchStarsNullRepo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only r0 is present; r1 is null (removed from data map).
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"r0": map[string]any{"stargazerCount": 10, "forkCount": 1},
		}})
	}))
	defer srv.Close()

	c := newStarsClient(t, srv.URL)
	out, err := c.FetchStars(context.Background(), []string{"a/one", "b/two"})
	if err != nil {
		t.Fatalf("FetchStars: %v", err)
	}
	if _, ok := out["b/two"]; ok {
		t.Fatalf("null repo should be absent, got %+v", out)
	}
	if out["a/one"].Stars != 10 {
		t.Fatalf("present repo wrong: %+v", out)
	}
}

// TestFetchStarsGraphQLError surfaces an all-error response as an error.
func TestFetchStarsGraphQLError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":   map[string]any{},
			"errors": []map[string]any{{"type": "FORBIDDEN", "message": "bad creds"}},
		})
	}))
	defer srv.Close()

	c := newStarsClient(t, srv.URL)
	if _, err := c.FetchStars(context.Background(), []string{"a/b"}); err == nil {
		t.Fatal("expected error for all-error graphql response")
	}
}

// TestBackfillHistoryPagination verifies page walking and weekly bucket parsing.
func TestBackfillHistoryPagination(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/stargazers/history") {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		pages++
		cursor := r.URL.Query().Get("cursor")
		switch cursor {
		case "":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"weeks":   []map[string]any{{"weekStart": "2026-10-27", "stars": 70, "days": 7}},
				"hasNext": true,
				"cursor":  "p2",
			})
		case "p2":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"weeks":   []map[string]any{{"weekStart": "2026-11-03", "stars": 20, "days": 3}},
				"hasNext": false,
			})
		default:
			t.Fatalf("unexpected cursor %q", cursor)
		}
	}))
	defer srv.Close()

	c := newStarsClient(t, srv.URL)
	pts, err := c.BackfillHistory(context.Background(), "golang/go")
	if err != nil {
		t.Fatalf("BackfillHistory: %v", err)
	}
	if pages != 2 {
		t.Fatalf("pages = %d, want 2", pages)
	}
	if len(pts) != 2 {
		t.Fatalf("points = %d, want 2", len(pts))
	}
	if pts[0].WeekStart != "2026-10-27" || pts[0].Stars != 70 || pts[0].Days != 7 {
		t.Fatalf("first bucket wrong: %+v", pts[0])
	}
	if pts[1].Days != 3 {
		t.Fatalf("partial-week days = %d, want 3", pts[1].Days)
	}
}

// TestBackfillHistoryInvalidName rejects a malformed full name before any request.
func TestBackfillHistoryInvalidName(t *testing.T) {
	c := newStarsClient(t, "http://127.0.0.1:0")
	if _, err := c.BackfillHistory(context.Background(), "not-a-full-name"); err == nil {
		t.Fatal("expected error for invalid full name")
	}
}

// TestBackfillHistoryBestEffort: a mid-walk failure returns what was gathered.
func TestBackfillHistoryPartialOnError(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		if pages == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"weeks":   []map[string]any{{"weekStart": "2026-10-27", "stars": 70, "days": 7}},
				"hasNext": true,
				"cursor":  "p2",
			})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newStarsClient(t, srv.URL)
	pts, err := c.BackfillHistory(context.Background(), "golang/go")
	if err == nil {
		t.Fatal("expected error on second page")
	}
	if len(pts) != 1 || pts[0].Stars != 70 {
		t.Fatalf("should keep first page on later failure, got %+v", pts)
	}
}

// countAliases counts "rN:" aliases in a generated query.
func countAliases(query string) int {
	n := 0
	for i := 0; ; i++ {
		if strings.Contains(query, aliasFor(i)+":") {
			n++
			continue
		}
		return n
	}
}
