package pr

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/morehao/trendradar/internal/ipc"
)

func TestParseRemote(t *testing.T) {
	cases := []struct {
		in        string
		owner     string
		repo      string
		shouldErr bool
	}{
		{"git@github.com:morehao/trendradar.git", "morehao", "trendradar", false},
		{"https://github.com/morehao/trendradar.git", "morehao", "trendradar", false},
		{"https://x-access-token@github.com/morehao/trendradar", "morehao", "trendradar", false},
		{"/tmp/local/remote.git", "", "", true},
	}
	for _, c := range cases {
		owner, repo, err := ParseRemote(c.in)
		if c.shouldErr {
			if err == nil {
				t.Errorf("%q: expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if owner != c.owner || repo != c.repo {
			t.Errorf("%q -> %s/%s, want %s/%s", c.in, owner, repo, c.owner, c.repo)
		}
	}
}

func newPullServer(t *testing.T, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/widgets/pulls" {
			t.Errorf("path = %q", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("state") != "all" || q.Get("head") != "acme:trending/2026-10-31" || q.Get("base") != "main" {
			t.Errorf("unexpected query: %v", q)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	c := New("acme", "widgets", "tok")
	c.BaseURL = srv.URL
	return c
}

func TestFindForBranchMerged(t *testing.T) {
	c := newPullServer(t, `[{"number":5,"html_url":"https://github.com/acme/widgets/pull/5","state":"closed","merged_at":"2026-01-02T03:04:05Z"}]`)
	p, err := c.FindForBranch(context.Background(), "trending/2026-10-31", "main")
	if err != nil {
		t.Fatal(err)
	}
	if p == nil || p.Number != 5 || !p.Merged() {
		t.Fatalf("unexpected pull: %+v", p)
	}
	if p.MergedAt == nil || p.MergedAt.UTC().Year() != 2026 {
		t.Fatalf("merged_at not parsed: %+v", p.MergedAt)
	}
}

func TestFindForBranchClosedUnmerged(t *testing.T) {
	c := newPullServer(t, `[{"number":3,"state":"closed"}]`)
	p, err := c.FindForBranch(context.Background(), "trending/2026-10-31", "main")
	if err != nil {
		t.Fatal(err)
	}
	if p == nil || p.Number != 3 || p.Merged() {
		t.Fatalf("closed-unmerged PR should not be Merged(): %+v", p)
	}
}

func TestFindForBranchNone(t *testing.T) {
	c := newPullServer(t, `[]`)
	p, err := c.FindForBranch(context.Background(), "trending/2026-10-31", "main")
	if err != nil {
		t.Fatal(err)
	}
	if p != nil {
		t.Fatalf("want nil, got %+v", p)
	}
}

func TestDeleteBranch(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		shouldErr bool
	}{
		{"ok 204", 204, false},
		{"already gone 422", 422, false},
		{"not found 404", 404, false},
		{"server error 500", 500, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete {
					t.Errorf("method = %s, want DELETE", r.Method)
				}
				if r.URL.Path != "/repos/acme/widgets/git/refs/heads/trending/daily/2026-10-30" {
					t.Errorf("path = %q", r.URL.Path)
				}
				w.WriteHeader(c.status)
			}))
			t.Cleanup(srv.Close)
			cl := New("acme", "widgets", "tok")
			cl.BaseURL = srv.URL
			err := cl.DeleteBranch(context.Background(), "trending/daily/2026-10-30")
			if c.shouldErr && err == nil {
				t.Fatalf("expected error for status %d", c.status)
			}
			if !c.shouldErr && err != nil {
				t.Fatalf("unexpected error for status %d: %v", c.status, err)
			}
		})
	}
}

func TestGetParsesMergeable(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantFalse bool
		wantNil   bool
	}{
		{"mergeable true", `{"number":7,"state":"open","mergeable":true,"mergeable_state":"clean"}`, false, false},
		{"mergeable false", `{"number":7,"state":"open","mergeable":false,"mergeable_state":"dirty"}`, true, false},
		{"mergeable null", `{"number":7,"state":"open","mergeable":null,"mergeable_state":"unknown"}`, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/repos/acme/widgets/pulls/7" {
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(srv.Close)
			c := New("acme", "widgets", "tok")
			c.BaseURL = srv.URL
			p, err := c.Get(context.Background(), 7)
			if err != nil {
				t.Fatal(err)
			}
			if p.Number != 7 {
				t.Fatalf("number = %d", p.Number)
			}
			if tc.wantNil && p.Mergeable != nil {
				t.Fatalf("want nil mergeable, got %v", *p.Mergeable)
			}
			if !tc.wantNil && p.Mergeable == nil {
				t.Fatal("want a mergeable value, got nil")
			}
			if p.MergeableFalse() != tc.wantFalse {
				t.Fatalf("MergeableFalse() = %v, want %v", p.MergeableFalse(), tc.wantFalse)
			}
		})
	}
}

// Merge must keep mapping GitHub's 405 (not mergeable) to NOT_MERGEABLE, so the
// caller surfaces a clear reason rather than a generic PR failure.
func TestMergeMaps405ToNotMergeable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/repos/acme/widgets/pulls/7/merge" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = io.WriteString(w, `{"message":"Pull Request is not mergeable"}`)
	}))
	t.Cleanup(srv.Close)
	c := New("acme", "widgets", "tok")
	c.BaseURL = srv.URL
	err := c.Merge(context.Background(), 7)
	if err == nil {
		t.Fatal("expected an error for 405")
	}
	var ei *ipc.ErrorInfo
	if !errors.As(err, &ei) || ei.Code != ipc.CodeNotMergeable {
		t.Fatalf("err = %v, want NOT_MERGEABLE", err)
	}
}
