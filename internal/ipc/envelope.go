// Package ipc defines the wire envelope shared between the trendradar core
// binary and its DSH plugin consumer. The types are aliases of the shared
// github.com/morehao/trendradar/contract package so there is one source of truth.
package ipc

import contract "github.com/morehao/trendradar/contract"

// SchemaVersion is the IPC envelope version (second generation).
const SchemaVersion = 2

// Command enumerates the sub-commands the core exposes over IPC.
type Command string

const (
	CommandRun     Command = "run"
	CommandServe   Command = "serve"
	CommandConfig  Command = "config"
	CommandVersion Command = "version"
)

// Aliases to the contract package (single source of truth).
type (
	RunRequest   = contract.RunRequest
	FileArtifact = contract.FileArtifact
	RunResult    = contract.RunResult
	State        = contract.State
	StateRun     = contract.StateRun
)

// ErrorInfo mirrors contract.ErrorInfo but adds the error interface so the core
// can return it as an error internally.
type ErrorInfo contract.ErrorInfo

// Error implements the error interface.
func (e *ErrorInfo) Error() string {
	if e == nil {
		return ""
	}
	return string(e.Code) + ": " + e.Message
}

// ToContract converts to the wire type.
func (e *ErrorInfo) ToContract() *contract.ErrorInfo { return (*contract.ErrorInfo)(e) }
