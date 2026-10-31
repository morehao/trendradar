package subscription

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingIsEmpty(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(filepath.Join(dir, "subscriptions.yaml"))
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("List = %v, want empty", got)
	}
}

func TestAddListRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subscriptions.yaml")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	added, err := s.Add("morehao/trendradar", "morehao/golib")
	if err != nil || added != 2 {
		t.Fatalf("Add = %d, %v; want 2, nil", added, err)
	}
	// duplicate add is idempotent
	added, err = s.Add("morehao/trendradar")
	if err != nil || added != 0 {
		t.Fatalf("dup Add = %d, %v; want 0, nil", added, err)
	}
	// re-load sees persisted list
	s2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.List(); len(got) != 2 || got[0] != "morehao/trendradar" || got[1] != "morehao/golib" {
		t.Fatalf("reload List = %v", got)
	}
	if err := s2.Remove("morehao/golib"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := s2.Remove("morehao/golib"); !errors.Is(err, ErrNotSubscribed) {
		t.Fatalf("Remove missing err = %v, want ErrNotSubscribed", err)
	}
	if got := s2.List(); len(got) != 1 || got[0] != "morehao/trendradar" {
		t.Fatalf("after remove List = %v", got)
	}
}

func TestAddRejectsInvalidWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subscriptions.yaml")
	s, _ := Load(path)
	if _, err := s.Add("good/repo"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add("other/repo", "bad//name"); err == nil {
		t.Fatal("expected invalid input error")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("file changed on invalid add:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestResolveSubscriptionsPath(t *testing.T) {
	// absolute override wins
	abs, err := ResolveSubscriptionsPath("/tmp/x/config.yaml", "/etc/subs.yaml")
	if err != nil || abs != "/etc/subs.yaml" {
		t.Fatalf("abs = %q, %v", abs, err)
	}
	// relative resolves against config dir
	got, err := ResolveSubscriptionsPath("/tmp/x/config.yaml", "subs.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if want := "/tmp/x/subs.yaml"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// empty configurable falls back to DefaultFile next to config
	got, err = ResolveSubscriptionsPath("/tmp/x/config.yaml", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := "/tmp/x/" + DefaultFile; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
