package fetch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListReleasesPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("page"); got != "1" {
			t.Errorf("page = %q, want 1", got)
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing auth header, got %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"id":2,"tag_name":"v2","name":"v2","body":"notes","prerelease":false,"draft":false,"html_url":"u2","published_at":"2026-10-31T00:00:00Z"},{"id":1,"tag_name":"v1","body":"old","html_url":"u1"}]`)
	}))
	defer srv.Close()

	c := NewClient(Options{BaseURL: srv.URL, APIBaseURL: srv.URL, Token: "tok"})
	pg, err := c.ListReleasesPage(context.Background(), "o", "r", 1, 2)
	if err != nil {
		t.Fatalf("ListReleasesPage: %v", err)
	}
	if len(pg.Releases) != 2 || pg.Releases[0].TagName != "v2" {
		t.Fatalf("releases = %+v", pg.Releases)
	}
	if !pg.HasNext {
		t.Fatal("HasNext = false, want true for a full page")
	}
}

func TestListTagsPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"name":"v1","commit":{"sha":"abc"}}]`)
	}))
	defer srv.Close()
	c := NewClient(Options{BaseURL: srv.URL, APIBaseURL: srv.URL})
	pg, err := c.ListTagsPage(context.Background(), "o", "r", 1, 30)
	if err != nil {
		t.Fatalf("ListTagsPage: %v", err)
	}
	if len(pg.Tags) != 1 || pg.Tags[0].Name != "v1" || pg.Tags[0].Commit.SHA != "abc" {
		t.Fatalf("tags = %+v", pg.Tags)
	}
	if pg.HasNext {
		t.Fatal("HasNext = true for a short page")
	}
}
