package fetch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/morehao/trendradar/internal/ipc"
)

func headerWith(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

// GitHub answers an exhausted REST quota with 403 (not only 429). Classifying
// it as a generic fetch failure makes the release stage count it as "failed"
// and the panel say "失败" instead of "限流".
func TestStatusErrorClassifiesRateLimit(t *testing.T) {
	cases := []struct {
		name   string
		code   int
		header http.Header
		body   string
		want   ipc.Code
	}{
		{"429 too many requests", http.StatusTooManyRequests, nil, "", ipc.CodeFetchRateLimited},
		{"403 primary quota", http.StatusForbidden, headerWith("X-RateLimit-Remaining", "0"), "", ipc.CodeFetchRateLimited},
		{"403 secondary quota", http.StatusForbidden, headerWith("Retry-After", "60"), "", ipc.CodeFetchRateLimited},
		{"403 quota message", http.StatusForbidden, nil, `{"message":"API rate limit exceeded for 1.2.3.4"}`, ipc.CodeFetchRateLimited},
		{"403 genuine forbidden", http.StatusForbidden, headerWith("X-RateLimit-Remaining", "42"), `{"message":"Resource not accessible by integration"}`, ipc.CodeFetchFailed},
		{"404 not found", http.StatusNotFound, nil, "", ipc.CodeFetchFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := statusError(tc.code, tc.header, []byte(tc.body))
			if got.Code != tc.want {
				t.Fatalf("code=%v message=%q, want %v", got.Code, got.Message, tc.want)
			}
		})
	}
}

// End to end: a 403 quota response on the releases endpoint must surface as a
// ratelimited *Error so releasewatch classifies it as StatusRateLimited.
func TestListReleasesPageRateLimited403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"API rate limit exceeded for 1.2.3.4."}`)
	}))
	defer srv.Close()

	c := NewClient(Options{BaseURL: srv.URL, APIBaseURL: srv.URL})
	_, err := c.ListReleasesPage(context.Background(), "o", "r", 1, 10)
	if err == nil {
		t.Fatal("want an error")
	}
	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatalf("error type %T, want *Error", err)
	}
	if fe.Code != ipc.CodeFetchRateLimited {
		t.Fatalf("code=%v, want %v", fe.Code, ipc.CodeFetchRateLimited)
	}
}
