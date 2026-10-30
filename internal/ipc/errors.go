package ipc

import "fmt"

// Code is a stable, machine-readable error code carried over IPC. Codes are
// SCREAMING_SNAKE strings so the Go core and the TypeScript plugin can switch
// on them without sharing numeric constants.
type Code string

// The canonical error codes. Keep this list in sync with contract/schema/ipc.v1.json.
const (
	CodeInternal           Code = "INTERNAL"
	CodeConfigInvalid      Code = "CONFIG_INVALID"
	CodePayloadTooLarge    Code = "PAYLOAD_TOO_LARGE"
	CodeFetchFailed        Code = "FETCH_FAILED"
	CodeFetchRateLimited   Code = "FETCH_RATE_LIMITED"
	CodeParseFailed        Code = "PARSE_FAILED"
	CodeModelUnauthorized  Code = "MODEL_UNAUTHORIZED"
	CodeModelRateLimited   Code = "MODEL_RATE_LIMITED"
	CodeModelTimeout       Code = "MODEL_TIMEOUT"
	CodeModelFailed        Code = "MODEL_FAILED"
	CodeModelBadOutput     Code = "MODEL_BAD_OUTPUT"
	CodeGitNotInstalled    Code = "GIT_NOT_INSTALLED"
	CodeRepoNotGit         Code = "REPO_NOT_GIT"
	CodeRepoDirty          Code = "REPO_DIRTY"
	CodeGitOperationFailed Code = "GIT_OPERATION_FAILED"
	CodePRFailed           Code = "PR_FAILED"
	CodeNotMergeable       Code = "NOT_MERGEABLE"
)

// AllCodes is the canonical, ordered list of every code. Tests assert its
// length and that it matches the retriable table exactly.
var AllCodes = []Code{
	CodeInternal,
	CodeConfigInvalid,
	CodePayloadTooLarge,
	CodeFetchFailed,
	CodeFetchRateLimited,
	CodeParseFailed,
	CodeModelUnauthorized,
	CodeModelRateLimited,
	CodeModelTimeout,
	CodeModelFailed,
	CodeModelBadOutput,
	CodeGitNotInstalled,
	CodeRepoNotGit,
	CodeRepoDirty,
	CodeGitOperationFailed,
	CodePRFailed,
	CodeNotMergeable,
}

// retriableCodes records every code and whether the caller may retry it. Being
// the single source of truth keeps Valid() and Retriable() consistent.
var retriableCodes = map[Code]bool{
	CodeInternal:           true, // transient panics/IO may succeed on retry
	CodeConfigInvalid:      false,
	CodePayloadTooLarge:    false,
	CodeFetchFailed:        false, // fetch retries internally; a final failure is terminal
	CodeFetchRateLimited:   true,
	CodeParseFailed:        false,
	CodeModelUnauthorized:  false,
	CodeModelRateLimited:   true,
	CodeModelTimeout:       true,
	CodeModelFailed:        true,
	CodeModelBadOutput:     false,
	CodeGitNotInstalled:    false,
	CodeRepoNotGit:         false,
	CodeRepoDirty:          false,
	CodeGitOperationFailed: true,
	CodePRFailed:           true,
	CodeNotMergeable:       false,
}

// Retriable reports whether the caller may retry an operation that failed with c.
func Retriable(c Code) bool { return retriableCodes[c] }

// Valid reports whether c is one of the canonical codes.
func (c Code) Valid() bool {
	_, ok := retriableCodes[c]
	return ok
}

// String implements fmt.Stringer.
func (c Code) String() string { return string(c) }

// NewError builds an ErrorInfo, deriving Retriable from the code table.
func NewError(c Code, format string, args ...any) *ErrorInfo {
	return &ErrorInfo{
		Code:      c,
		Message:   fmt.Sprintf(format, args...),
		Retriable: Retriable(c),
	}
}
