package ipc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestContractVersionMatchesPackage guards the handshake's self-reported
// contractVersion against silent drift: it must equal contract/package.json's
// version (the package that the core builds against via the local `replace`).
// The wire integers are independent, so this is a documentation/self-report
// field — exactly the kind that drifts unnoticed when nothing asserts it.
func TestContractVersionMatchesPackage(t *testing.T) {
	// internal/ipc -> repo root is two levels up.
	path := filepath.Join("..", "..", "contract", "package.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if pkg.Version == "" {
		t.Fatalf("%s has no version field", path)
	}
	if ContractVersion != pkg.Version {
		t.Fatalf("ContractVersion = %q, but contract/package.json version = %q; keep them in sync", ContractVersion, pkg.Version)
	}
}
