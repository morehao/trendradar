// Package ipc defines the wire envelope shared between the trendradar core
// binary and its DSH plugin consumer. Field names are camelCase to match the
// TypeScript side; every payload carries an explicit ipcSchema version.
package ipc

import "time"

// SchemaVersion is the IPC envelope version. Bumped only on breaking changes.
const SchemaVersion = 1

// Command enumerates the sub-commands the core exposes over IPC.
type Command string

const (
	CommandRun     Command = "run"
	CommandServe   Command = "serve"
	CommandConfig  Command = "config"
	CommandVersion Command = "version"
)

// RunRequest is the ONLY envelope struct that forbids extension fields
// (additionalProperties: false). Everything else tolerates extra keys so the
// plugin can roll forward without a core release.
type RunRequest struct {
	IPCSchema  int     `json:"ipcSchema"`
	Command    Command `json:"command"`
	ConfigPath string  `json:"configPath,omitempty"`
	Date       string  `json:"date,omitempty"`
	DryRun     bool    `json:"dryRun,omitempty"`
	OutputDir  string  `json:"outputDir,omitempty"`
}

// FileArtifact describes one rendered output file, without its contents.
type FileArtifact struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// ErrorInfo is the machine-readable error carried by RunResult.
type ErrorInfo struct {
	Code      Code           `json:"code"`
	Message   string         `json:"message"`
	Retriable bool           `json:"retriable"`
	Details   map[string]any `json:"details,omitempty"`
}

// RunResult is the envelope returned by a `run` invocation.
type RunResult struct {
	IPCSchema  int            `json:"ipcSchema"`
	OK         bool           `json:"ok"`
	Command    Command        `json:"command"`
	StartedAt  time.Time      `json:"startedAt"`
	FinishedAt time.Time      `json:"finishedAt"`
	Skipped    bool           `json:"skipped"`
	Files      []FileArtifact `json:"files"`
	Error      *ErrorInfo     `json:"error,omitempty"`
	Warnings   []string       `json:"warnings,omitempty"`
}

// State is the small bookkeeping payload the core reads back between runs to
// decide idempotency (same contentHash -> skip).
type State struct {
	IPCSchema int       `json:"ipcSchema"`
	LastRun   *StateRun `json:"lastRun,omitempty"`
}

// StateRun records the last successful run's digest identity.
type StateRun struct {
	Date        string    `json:"date"`
	ContentHash string    `json:"contentHash"`
	RunAt       time.Time `json:"runAt"`
}
