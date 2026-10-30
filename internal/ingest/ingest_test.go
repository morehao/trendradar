package ingest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/morehao/trendradar/internal/fetch"
	"github.com/morehao/trendradar/internal/ipc"
)

func TestIngestFallsBackWhenHTMLHasNoRows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/trending" {
			_, _ = w.Write([]byte(`<html><body><p>no rows here</p></body></html>`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"full_name":"acme/one","html_url":"https://github.com/acme/one","description":"d","language":"Go","stargazers_count":10,"forks_count":1}]}`))
	}))
	defer srv.Close()

	c := fetch.NewClient(fetch.Options{BaseURL: srv.URL, APIBaseURL: srv.URL})
	res, err := Ingest(context.Background(), c, []string{"all"}, "daily", 5)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if !res.Degraded {
		t.Fatal("expected Degraded=true")
	}
	if len(res.Repos) != 1 || res.Repos[0].Source != "search-api" {
		t.Fatalf("unexpected repos: %+v", res.Repos)
	}
}

func TestIngestBothPathsFailReturnsFetchFailed(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := fetch.NewClient(fetch.Options{BaseURL: srv.URL, APIBaseURL: srv.URL, RetryInterval: 5 * time.Millisecond, MaxRetry: 2})
	_, err := Ingest(context.Background(), c, []string{"all"}, "daily", 5)
	if err == nil {
		t.Fatal("expected error")
	}
	var fe *fetch.Error
	if !errors.As(err, &fe) || fe.Code != ipc.CodeFetchFailed {
		t.Fatalf("err = %v, want FETCH_FAILED", err)
	}
}
