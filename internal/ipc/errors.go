package ipc

import (
	"fmt"

	contract "github.com/morehao/trendradar/contract"
)

// Code is the contract error code type (string backed).
type Code = contract.ErrorCode

// The canonical error codes, aliased from the contract catalog.
const (
	CodeInternal           = contract.ErrorCodeINTERNAL
	CodeConfigInvalid      = contract.ErrorCodeCONFIG_INVALID
	CodePayloadTooLarge    = contract.ErrorCodePAYLOAD_TOO_LARGE
	CodeFetchFailed        = contract.ErrorCodeFETCH_FAILED
	CodeFetchRateLimited   = contract.ErrorCodeFETCH_RATE_LIMITED
	CodeParseFailed        = contract.ErrorCodePARSE_FAILED
	CodeModelUnauthorized  = contract.ErrorCodeMODEL_UNAUTHORIZED
	CodeModelRateLimited   = contract.ErrorCodeMODEL_RATE_LIMITED
	CodeModelTimeout       = contract.ErrorCodeMODEL_TIMEOUT
	CodeModelFailed        = contract.ErrorCodeMODEL_FAILED
	CodeModelBadOutput     = contract.ErrorCodeMODEL_BAD_OUTPUT
	CodeGitNotInstalled    = contract.ErrorCodeGIT_NOT_INSTALLED
	CodeRepoNotGit         = contract.ErrorCodeREPO_NOT_GIT
	CodeRepoDirty          = contract.ErrorCodeREPO_DIRTY
	CodeGitOperationFailed = contract.ErrorCodeGIT_OPERATION_FAILED
	CodePRFailed           = contract.ErrorCodePR_FAILED
	CodeNotMergeable       = contract.ErrorCodeNOT_MERGEABLE
)

// AllCodes is the canonical, ordered list of every code.
var AllCodes = []Code{
	CodeInternal, CodeConfigInvalid, CodePayloadTooLarge, CodeFetchFailed,
	CodeFetchRateLimited, CodeParseFailed, CodeModelUnauthorized, CodeModelRateLimited,
	CodeModelTimeout, CodeModelFailed, CodeModelBadOutput, CodeGitNotInstalled,
	CodeRepoNotGit, CodeRepoDirty, CodeGitOperationFailed, CodePRFailed, CodeNotMergeable,
}

// Retriable reports whether the caller may retry an operation that failed with c.
func Retriable(c Code) bool { return c.Retriable() }

// NewError builds an ErrorInfo, deriving Retriable from the contract catalog.
func NewError(c Code, format string, args ...any) *ErrorInfo {
	return &ErrorInfo{Code: c, Message: fmt.Sprintf(format, args...), Retriable: c.Retriable()}
}
