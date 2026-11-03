package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/morehao/trendradar/internal/ipc"
)

func TestRetryOn429ThenSuccess(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<html><article class="Box-row"></article></html>`))
	}))
	defer srv.Close()

	c := NewClient(Options{BaseURL: srv.URL, APIBaseURL: srv.URL, RetryInterval: 10 * time.Millisecond, MaxRetry: 2})
	body, err := c.getHTML(context.Background(), "/trending")
	if err != nil {
		t.Fatalf("getHTML: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("empty body")
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestFetchFailedAfterRetries(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient(Options{BaseURL: srv.URL, APIBaseURL: srv.URL, RetryInterval: 10 * time.Millisecond, MaxRetry: 2})
	_, err := c.getHTML(context.Background(), "/trending")
	if err == nil {
		t.Fatal("expected error")
	}
	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatalf("error type = %T, want *fetch.Error", err)
	}
	if fe.Code != ipc.CodeFetchFailed {
		t.Fatalf("code = %s, want FETCH_FAILED", fe.Code)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2 (1 initial + 1 retry)", got)
	}
}

func TestSearchFallbackMaps(t *testing.T) {
	var gotQueries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQueries = append(gotQueries, r.URL.Query().Get("q"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[
			{"full_name":"acme/one","html_url":"https://github.com/acme/one","description":"first","language":"Go","stargazers_count":1200,"forks_count":30},
			{"full_name":"acme/two","html_url":"https://github.com/acme/two","description":"second","language":null,"stargazers_count":800,"forks_count":10}
		]}`))
	}))
	defer srv.Close()

	c := NewClient(Options{BaseURL: srv.URL, APIBaseURL: srv.URL})
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	repos, err := c.SearchFallbackAt(context.Background(), now, []string{"go"}, "daily")
	if err != nil {
		t.Fatalf("SearchFallbackAt: %v", err)
	}
	if len(gotQueries) != 1 || gotQueries[0] != "pushed:>2026-10-29 language:go" {
		t.Fatalf("queries = %q, want [pushed:>2026-10-29 language:go]", gotQueries)
	}
	if len(repos) != 2 {
		t.Fatalf("len = %d, want 2", len(repos))
	}
	if repos[0].FullName != "acme/one" || repos[0].Language != "go" || repos[0].Stars != 1200 || repos[0].StarsInPeriod != 0 || repos[0].Rank != 1 {
		t.Fatalf("unexpected first repo: %+v", repos[0])
	}
	if repos[0].Source != "search-api" {
		t.Fatalf("source = %s, want search-api", repos[0].Source)
	}
	if repos[1].Language != "unknown" {
		t.Fatalf("null language => %q, want unknown", repos[1].Language)
	}
}

// TestFetchTrendingToleratesPartialFailure verifies one failed language does not
// discard the successfully fetched pages.
func TestFetchTrendingToleratesPartialFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/trending/python" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`<html><article class="Box-row"></article></html>`))
	}))
	defer srv.Close()

	c := NewClient(Options{BaseURL: srv.URL, APIBaseURL: srv.URL, RetryInterval: 5 * time.Millisecond, MaxRetry: 1})
	res, err := c.FetchTrending(context.Background(), []string{"go", "python"}, "daily")
	if err != nil {
		t.Fatalf("FetchTrending: %v", err)
	}
	if !res.Any() || len(res.Pages) != 1 {
		t.Fatalf("pages = %d, want 1", len(res.Pages))
	}
	if _, ok := res.Pages["go"]; !ok {
		t.Fatal("expected go page present")
	}
	if len(res.FailedLangs) != 1 || res.FailedLangs[0] != "python" {
		t.Fatalf("FailedLangs = %v, want [python]", res.FailedLangs)
	}
}
