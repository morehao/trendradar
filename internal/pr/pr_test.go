package pr

import "testing"

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
