package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func yamlUnmarshal(t *testing.T, text string, v any) error {
	t.Helper()
	return yaml.Unmarshal([]byte(text), v)
}

// A13: the auto formulas are part of the contract with the plugin, so they are
// asserted directly rather than inferred from a run.
func TestAutoConcurrency(t *testing.T) {
	auto := AutoInt{Auto: true}
	cases := []struct{ n, want int }{
		{0, 4}, {1, 4}, {39, 4}, {40, 4}, {41, 5}, {100, 8}, {1000, 8},
	}
	for _, c := range cases {
		if got := auto.Resolve(c.n, 4, 8); got != c.want {
			t.Fatalf("n=%d -> %d, want %d", c.n, got, c.want)
		}
	}
	// An explicit number always wins.
	if got := (AutoInt{Value: 2}).Resolve(100, 4, 8); got != 2 {
		t.Fatalf("explicit -> %d, want 2", got)
	}
}

func TestAutoBudget(t *testing.T) {
	auto := AutoInt{Auto: true}
	cases := []struct{ n, want int }{
		{1, 33}, {40, 150}, {90, 300}, {100, 300},
	}
	for _, c := range cases {
		if got := auto.ResolveBudget(c.n, 300); got != c.want {
			t.Fatalf("n=%d -> %d, want %d", c.n, got, c.want)
		}
	}
	if got := (AutoInt{Value: 60}).ResolveBudget(100, 300); got != 60 {
		t.Fatalf("explicit -> %d, want 60", got)
	}
}

func TestAutoIntUnmarshal(t *testing.T) {
	var v ReleasesConfig
	if err := yamlUnmarshal(t, "concurrency: auto\nbudgetSeconds: auto\n", &v); err != nil {
		t.Fatal(err)
	}
	if !v.Concurrency.Auto || !v.BudgetSeconds.Auto {
		t.Fatalf("auto not parsed: %+v", v)
	}
	if err := yamlUnmarshal(t, "concurrency: 6\nbudgetSeconds: 90\n", &v); err != nil {
		t.Fatal(err)
	}
	if v.Concurrency.Auto || v.Concurrency.Value != 6 || v.BudgetSeconds.Value != 90 {
		t.Fatalf("numbers not parsed: %+v", v)
	}
	if err := yamlUnmarshal(t, "concurrency: soon\n", &v); err == nil {
		t.Fatal("want an error for a non-numeric, non-auto value")
	}
}

func validBase() *Config {
	return &Config{
		Model:    ModelConfig{Provider: "fake", Name: "fake", BaseURL: "http://127.0.0.1:9/v1"},
		Repo:     RepoConfig{Path: "/tmp/repo"},
		Trending: TrendingConfig{Languages: []string{"go"}, Since: "daily"},
		Output:   OutputConfig{Dir: "digests"},
	}
}

func TestReleasePeriodRejectsDaily(t *testing.T) {
	c := validBase()
	c.Releases.Period = "daily"
	if err := c.Validate(); err == nil {
		t.Fatal("releases.period=daily must be CONFIG_INVALID: only weekly is implemented")
	}
	c.Releases.Period = "weekly"
	if err := c.Validate(); err != nil {
		t.Fatalf("weekly should validate: %v", err)
	}
}
