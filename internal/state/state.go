// Package state persists the last successful run so reruns with the same
// contentHash can be skipped. The file lives inside .git/ and is never tracked.
package state

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/morehao/trendradar/internal/ipc"
)

// Path returns the state file location for a repo work tree.
func Path(repoDir string) string {
	return filepath.Join(repoDir, ".git", "trendradar-state.json")
}

// Load reads state, returning an empty one if the file does not exist.
func Load(repoDir string) (*ipc.State, error) {
	data, err := os.ReadFile(Path(repoDir))
	if os.IsNotExist(err) {
		return &ipc.State{IPCSchema: ipc.SchemaVersion}, nil
	}
	if err != nil {
		return nil, err
	}
	var s ipc.State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.IPCSchema == 0 {
		s.IPCSchema = ipc.SchemaVersion
	}
	return &s, nil
}

// Save writes state atomically enough for our single-writer use.
func Save(repoDir string, s *ipc.State) error {
	s.IPCSchema = ipc.SchemaVersion
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(repoDir), append(data, '\n'), 0o644)
}
