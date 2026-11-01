package subscription

import "testing"

func TestIsValidRepoName(t *testing.T) {
	good := []string{"vercel/next.js", "a/b", "morehao/dsh-github-trending", "tensorflow/tensorflow"}
	for _, r := range good {
		if !IsValidRepoName(r) {
			t.Errorf("IsValidRepoName(%q) = false, want true", r)
		}
	}
	bad := []string{"", "no-slash", "/b", "a/", "a/b/c", "a b/c", "a/b c"}
	for _, r := range bad {
		if IsValidRepoName(r) {
			t.Errorf("IsValidRepoName(%q) = true, want false", r)
		}
	}
}
