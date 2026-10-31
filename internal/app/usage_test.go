package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendUsageLogAppendsLines(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := UsageRecord{Time: time.Now().UTC(), Model: "m1", TotalTokens: 3, UsageKnown: true}
	if err := AppendUsageLog(dir, rec); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if err := AppendUsageLog(dir, rec); err != nil {
		t.Fatalf("second append: %v", err)
	}

	data, err := os.ReadFile(UsageLogPath(dir))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	var got UsageRecord
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Model != "m1" || got.TotalTokens != 3 || !got.UsageKnown {
		t.Fatalf("record = %+v", got)
	}
}

func TestAppendUsageLogSkipsNonGitDir(t *testing.T) {
	dir := t.TempDir()
	if err := AppendUsageLog(dir, UsageRecord{}); err != nil {
		t.Fatalf("append outside git: %v", err)
	}
	if _, err := os.Stat(UsageLogPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("usage log should not exist outside a git work tree, stat err = %v", err)
	}
}
