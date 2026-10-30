package pr

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
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
