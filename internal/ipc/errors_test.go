package ipc

import (
	"testing"

	contract "github.com/morehao/trendradar/contract"
)

func TestErrorCodeCount(t *testing.T) {
	if got, want := len(AllCodes), 17; got != want {
		t.Fatalf("AllCodes length = %d, want %d", got, want)
	}
	if len(contract.ErrorCatalog) != 17 {
		t.Fatalf("contract.ErrorCatalog length = %d, want 17", len(contract.ErrorCatalog))
	}
}

func TestEveryCodeValid(t *testing.T) {
	seen := make(map[Code]bool, len(AllCodes))
	for _, c := range AllCodes {
		if seen[c] {
			t.Fatalf("duplicate code %q", c)
		}
		seen[c] = true
		if !c.Valid() {
			t.Fatalf("code %q not valid", c)
		}
	}
	if Code("NOPE").Valid() {
		t.Fatal("unknown code reported valid")
	}
}

func TestRetriableSpotChecks(t *testing.T) {
	retriable := []Code{CodeFetchRateLimited, CodeModelRateLimited, CodeModelTimeout, CodeModelFailed, CodeGitOperationFailed, CodePRFailed, CodeInternal}
	for _, c := range retriable {
		if !Retriable(c) {
			t.Errorf("Retriable(%q) = false, want true", c)
		}
	}
	terminal := []Code{CodeConfigInvalid, CodeModelUnauthorized, CodeRepoDirty, CodeGitNotInstalled, CodeNotMergeable, CodePayloadTooLarge}
	for _, c := range terminal {
		if Retriable(c) {
			t.Errorf("Retriable(%q) = true, want false", c)
		}
	}
}

func TestNewErrorFillsRetriable(t *testing.T) {
	e := NewError(CodeFetchRateLimited, "status %d", 429)
	if e.Code != CodeFetchRateLimited || e.Message != "status 429" || !e.Retriable {
		t.Fatalf("unexpected error info: %+v", e)
	}
}
