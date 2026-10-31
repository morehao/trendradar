package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, ".env", "\ufeff# comment\nexport A=1\nB=\"two words\"\nC='three'\nD=raw # inline stays\n")
	got, err := LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got["A"] != "1" || got["B"] != "two words" || got["C"] != "three" {
		t.Fatalf("parsed = %#v", got)
	}
}

func TestApplyFileKeepsExistingEnv(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, ".env", "TRENDRADAR_TEST_KEEP=fromfile\nTRENDRADAR_TEST_SET=fromfile\n")
	t.Setenv("TRENDRADAR_TEST_KEEP", "fromenv")
	if err := ApplyFile(p); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("TRENDRADAR_TEST_KEEP"); got != "fromenv" {
		t.Fatalf("existing env overwritten: %q", got)
	}
	if got := os.Getenv("TRENDRADAR_TEST_SET"); got != "fromfile" {
		t.Fatalf("missing value not set: %q", got)
	}
}

func TestFindNearestWalksUp(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".env", "X=1\n")
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := FindNearest(sub)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(root, ".env") {
		t.Fatalf("nearest = %q", got)
	}
}
