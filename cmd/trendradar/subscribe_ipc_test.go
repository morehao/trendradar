package main

import (
	"strings"
	"testing"

	"github.com/morehao/trendradar/internal/ipc"
)

func strp(s string) *string { return &s }

func TestSubscribeIPCRejectsMissingConfigPath(t *testing.T) {
	req := ipc.RunRequest{Command: "subscribe", Action: strp("add"), Repo: strp("o/r")}
	if _, err := validateSubscribeRequest(req); err == nil {
		t.Fatal("expected error for missing configPath")
	} else if !strings.Contains(err.Error(), "configPath") {
		t.Fatalf("error = %v, want mention of configPath", err)
	}
}

func TestSubscribeIPCRequiresAction(t *testing.T) {
	req := ipc.RunRequest{Command: "subscribe", ConfigPath: strp("/tmp/c.yaml")}
	if _, err := validateSubscribeRequest(req); err == nil {
		t.Fatal("expected error for missing action")
	}
}

func TestSubscribeIPCRequiresRepoForWrite(t *testing.T) {
	for _, action := range []string{"add", "remove"} {
		req := ipc.RunRequest{Command: "subscribe", Action: strp(action), ConfigPath: strp("/tmp/c.yaml")}
		if _, err := validateSubscribeRequest(req); err == nil {
			t.Fatalf("%s: expected error for missing repo", action)
		}
	}
}

func TestSubscribeIPCListNeedsNoRepo(t *testing.T) {
	req := ipc.RunRequest{Command: "subscribe", Action: strp("list"), ConfigPath: strp("/tmp/c.yaml")}
	if _, err := validateSubscribeRequest(req); err != nil {
		t.Fatalf("list should be valid: %v", err)
	}
}

func TestSubscribeIPCWrongCommand(t *testing.T) {
	req := ipc.RunRequest{Command: "run", Action: strp("list"), ConfigPath: strp("/tmp/c.yaml")}
	if _, err := validateSubscribeRequest(req); err == nil {
		t.Fatal("expected error for non-subscribe command")
	}
}

func TestClassifySubscribeError(t *testing.T) {
	cases := map[string]ipc.Code{
		"subscribe requires action":         ipc.CodeConfigInvalid,
		"subscribe add requires repo":       ipc.CodeConfigInvalid,
		"subscription: invalid repo \"x\"":  ipc.CodeConfigInvalid,
		"subscription: repo not subscribed": ipc.CodeConfigInvalid,
		"something unexpected exploded":     ipc.CodeInternal,
	}
	for msg, want := range cases {
		if got := classifySubscribeError(errString(msg)); got != want {
			t.Fatalf("%q -> %s, want %s", msg, got, want)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
