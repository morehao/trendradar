package main

import "testing"

func TestFilterReposToPresent(t *testing.T) {
	repos := []string{"a/one", "b/two", "c/three"}
	got, ok := filterReposTo(repos, "b/two")
	if !ok {
		t.Fatalf("expected ok for present repo")
	}
	if len(got) != 1 || got[0] != "b/two" {
		t.Fatalf("got %v, want [b/two]", got)
	}
}

func TestFilterReposToAbsent(t *testing.T) {
	repos := []string{"a/one", "b/two"}
	if _, ok := filterReposTo(repos, "z/nine"); ok {
		t.Fatalf("expected not ok for absent repo")
	}
	if _, ok := filterReposTo(nil, "a/one"); ok {
		t.Fatalf("expected not ok for empty list")
	}
}

func TestFilterReposToKeepsSingleMatch(t *testing.T) {
	// Defensive: even if the list somehow repeats the repo, the result is one entry.
	repos := []string{"a/one", "a/one"}
	got, ok := filterReposTo(repos, "a/one")
	if !ok || len(got) != 1 {
		t.Fatalf("got %v ok=%v, want single entry", got, ok)
	}
}
