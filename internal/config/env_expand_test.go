package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadExpandsEnvInModelAndRepo(t *testing.T) {
	t.Setenv("TRENDRADAR_TEST_MODEL", "deepseek-flash")
	t.Setenv("TRENDRADAR_TEST_KEY", "sk-test-value")
	t.Setenv("TRENDRADAR_TEST_REPO", t.TempDir())

	body := `model:
  provider: deepseek
  name: ${TRENDRADAR_TEST_MODEL}
  baseURL: https://api.deepseek.com/v1
  apiKey: ${TRENDRADAR_TEST_KEY}
repo:
  path: ${TRENDRADAR_TEST_REPO}
trending:
  languages: [all]
  since: daily
output:
  dir: digests
`
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Model.Name != "deepseek-flash" || c.Model.APIKey != "sk-test-value" {
		t.Fatalf("model not expanded: name=%q key=%q", c.Model.Name, c.Model.APIKey)
	}
	if c.Repo.Path != os.Getenv("TRENDRADAR_TEST_REPO") {
		t.Fatalf("repo path not expanded: %q", c.Repo.Path)
	}
}
