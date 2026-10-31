package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/morehao/trendradar/internal/ipc"
)

// runSubscribeIPC executes a contract RunRequest with command="subscribe".
// It shares the CLI implementation (runSubscribe) so both entry points behave
// identically; only the argument shape differs.
func runSubscribeIPC(req ipc.RunRequest) error {
	action := ""
	if req.Action != nil {
		action = *req.Action
	}
	args := []string{action}
	if req.Repo != nil {
		args = append(args, *req.Repo)
	}
	if req.ConfigPath != nil {
		args = append(args, "--config", *req.ConfigPath)
	}
	return runSubscribe(args)
}

// emitSubscribeResult writes a contract RunResult for a subscribe command.
func emitSubscribeResult(req ipc.RunRequest, started time.Time, err error) {
	out := ipc.RunResult{
		IPCSchema:  ipc.SchemaVersion,
		OK:         err == nil,
		Command:    "subscribe",
		StartedAt:  started.UTC(),
		FinishedAt: time.Now().UTC(),
		Skipped:    false,
		Files:      []ipc.FileArtifact{},
	}
	if err != nil {
		out.Error = ipc.NewError(classifySubscribeError(err), "%s", err.Error()).ToContract()
	}
	b, _ := json.Marshal(out)
	fmt.Println(string(b))
}

// classifySubscribeError maps a subscribe failure to a stable IPC code. Bad
// input (missing action/repo/configPath, invalid owner/repo) is a caller error,
// not an internal one.
func classifySubscribeError(err error) ipc.Code {
	msg := err.Error()
	for _, s := range []string{
		"requires action", "requires repo", "requires configPath",
		"invalid repo", "unknown subscribe action", "repo not subscribed",
	} {
		if strings.Contains(msg, s) {
			return ipc.CodeConfigInvalid
		}
	}
	return ipc.CodeInternal
}

// subscribeFromStdin reads a RunRequest from stdin, which is how a plugin
// invokes subscribe without depending on CLI flag syntax.
func subscribeFromStdin() (ipc.RunRequest, error) {
	var req ipc.RunRequest
	dec := json.NewDecoder(os.Stdin)
	if err := dec.Decode(&req); err != nil {
		return req, fmt.Errorf("decode RunRequest: %w", err)
	}
	return validateSubscribeRequest(req)
}

// validateSubscribeRequest enforces the rules the schema cannot express as Go
// types, and in particular refuses to guess where the subscription file lives:
// an IPC caller must point at the config so subscribe and run resolve the same
// path. Silently falling back to ./subscriptions.yaml would write outside the
// caller's data repo and depend on an unpredictable working directory.
func validateSubscribeRequest(req ipc.RunRequest) (ipc.RunRequest, error) {
	if req.Command != "subscribe" {
		return req, fmt.Errorf("command %q is not subscribe", req.Command)
	}
	if req.Action == nil || *req.Action == "" {
		return req, fmt.Errorf("subscribe requires action (list|add|remove)")
	}
	switch *req.Action {
	case "list":
		if req.Repo != nil {
			return req, fmt.Errorf("subscribe list does not take repo")
		}
	case "add", "remove":
		if req.Repo == nil || *req.Repo == "" {
			return req, fmt.Errorf("subscribe %s requires repo", *req.Action)
		}
	default:
		return req, fmt.Errorf("unknown subscribe action %q (want list|add|remove)", *req.Action)
	}
	if req.ConfigPath == nil || *req.ConfigPath == "" {
		return req, fmt.Errorf("subscribe over IPC requires configPath so run and subscribe resolve the same subscriptions file")
	}
	return req, nil
}
