package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	contract "github.com/morehao/trendradar/contract"
	"github.com/morehao/trendradar/internal/config"
	"github.com/morehao/trendradar/internal/ipc"
	"github.com/morehao/trendradar/internal/notify"
)

// runNotify implements `trendradar notify <test>`.
//
// It exists so a caller (the DSH plugin, or a human) can verify a webhook is
// actually reachable *before* waiting for a full run: a single fixed test
// message is sent through the exact same deliver/sign path a real push uses.
// It never fetches, summarizes, or touches the repo.
//
// Two invocation forms share one implementation:
//   - IPC (plugin path): `notify --json` reads a contract RunRequest from stdin
//     and writes a contract RunResult{command:"notify", notify:<report>}.
//   - CLI (human path): `notify test --config config.yaml [--channel x] [--json]`.
func runNotify(args []string) error {
	// IPC form: `notify --json` (a RunRequest arrives on stdin). Mirrors how
	// `subscribe --json` works so the plugin parses one envelope shape.
	if len(args) > 0 && (args[0] == "--json" || args[0] == "-json") {
		started := time.Now()
		req, rerr := notifyFromStdin()
		if rerr != nil {
			emitNotifyResult(started, nil, rerr)
			return rerr
		}
		rep, rerr := notifyFromRequest(req)
		emitNotifyResult(started, rep, rerr)
		return rerr
	}

	if len(args) == 0 {
		return fmt.Errorf("usage: trendradar notify test --config config.yaml [--channel lark|dingtalk|wecom] [--json]")
	}
	switch args[0] {
	case "test":
		return runNotifyTest(args[1:], os.Stdout)
	default:
		return fmt.Errorf("unknown notify subcommand %q (want: test)", args[0])
	}
}

// runNotifyTest is the human CLI path: parse flags, load config, send, print.
func runNotifyTest(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("notify test", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfgPath := fs.String("config", "", "path to config.yaml (required)")
	channel := fs.String("channel", "", "test only this channel: lark|dingtalk|wecom (default: every enabled channel)")
	asJSON := fs.Bool("json", false, "emit a contract RunResult (command=notify) as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		return ipc.NewError(ipc.CodeConfigInvalid, "notify test requires --config")
	}

	ch := strings.TrimSpace(strings.ToLower(*channel))
	if ch != "" && !isKnownNotifyChannel(ch) {
		return ipc.NewError(ipc.CodeConfigInvalid, "unknown channel %q (want: lark, dingtalk, wecom)", *channel)
	}

	started := time.Now()
	rep, err := notifyWithConfig(*cfgPath, ch)
	if *asJSON {
		emitNotifyResult(started, rep, err)
		return err
	}
	if err != nil {
		return err
	}
	return printNotifyReport(out, rep)
}

// notifyFromRequest runs a notify-test from an IPC RunRequest.
func notifyFromRequest(req ipc.RunRequest) (*contract.NotifyReport, error) {
	if req.Command != string(ipc.CommandNotify) {
		return nil, ipc.NewError(ipc.CodeConfigInvalid, "notify IPC requires command=notify (got %q)", req.Command)
	}
	if req.Action == nil || *req.Action != "test" {
		return nil, ipc.NewError(ipc.CodeConfigInvalid, "notify only supports action=test (got %v)", derefOr(req.Action, ""))
	}
	if req.ConfigPath == nil || *req.ConfigPath == "" {
		return nil, ipc.NewError(ipc.CodeConfigInvalid, "notify test requires configPath")
	}
	ch, err := channelOf(req.Channel)
	if err != nil {
		return nil, err
	}
	return notifyWithConfig(*req.ConfigPath, ch)
}

// derefOr returns *p or def when p is nil (for error messages).
func derefOr(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}

// channelOf validates an optional IPC channel pointer.
func channelOf(channel *string) (string, error) {
	if channel == nil || *channel == "" {
		return notify.TestChannelAll, nil
	}
	ch := strings.TrimSpace(strings.ToLower(*channel))
	if !isKnownNotifyChannel(ch) {
		return "", ipc.NewError(ipc.CodeConfigInvalid, "unknown channel %q (want: lark, dingtalk, wecom)", *channel)
	}
	return ch, nil
}

// notifyWithConfig loads config (expanding ${VAR}) and sends one test message.
func notifyWithConfig(cfgPath, channel string) (*contract.NotifyReport, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	opt := notify.Options{
		Provider: nil, // a test message never summarizes; no model needed
		Lang:     cfg.EffectiveOutputLanguage(),
		Cfg:      cfg.Notify,
	}
	rep := notify.TestDeliver(context.Background(), opt, channel)
	return &rep, nil
}

func isKnownNotifyChannel(name string) bool {
	switch name {
	case "lark", "dingtalk", "wecom":
		return true
	}
	return false
}

// emitNotifyResult writes a contract RunResult for a notify command. The report
// rides in RunResult.notify so the plugin reads the same envelope shape as run.
func emitNotifyResult(started time.Time, rep *contract.NotifyReport, err error) {
	res := ipc.RunResult{
		IPCSchema:  ipc.SchemaVersion,
		OK:         err == nil && rep != nil && reportAllSent(*rep),
		Command:    string(ipc.CommandNotify),
		StartedAt:  started.UTC(),
		FinishedAt: time.Now().UTC(),
		Skipped:    false,
		Files:      []ipc.FileArtifact{},
	}
	if rep != nil {
		res.Notify = rep
	}
	if err != nil {
		res.Error = ipc.NewError(classifyNotifyError(err), "%s", err.Error()).ToContract()
	}
	b, _ := json.Marshal(res)
	fmt.Println(string(b))
}

// classifyNotifyError maps a notify failure to a stable IPC code. Caller errors
// (bad channel/action/config) are CONFIG_INVALID; anything else is INTERNAL.
func classifyNotifyError(err error) ipc.Code {
	var ie *ipc.ErrorInfo
	if ie2, ok := err.(*ipc.ErrorInfo); ok {
		ie = ie2
	}
	if ie != nil {
		return ie.Code
	}
	msg := err.Error()
	for _, s := range []string{"requires", "unknown channel", "unknown notify", "only supports"} {
		if strings.Contains(msg, s) {
			return ipc.CodeConfigInvalid
		}
	}
	return ipc.CodeInternal
}

// reportAllSent reports whether every attempted (non-disabled) channel sent.
func reportAllSent(rep contract.NotifyReport) bool {
	if !rep.Attempted {
		return false
	}
	anyActive := false
	for _, m := range rep.Messages {
		for _, c := range m.Channels {
			if c.Status == "disabled" {
				continue
			}
			anyActive = true
			if c.Status != "sent" {
				return false
			}
		}
	}
	return anyActive
}

// printNotifyReport is the human-facing CLI output.
func printNotifyReport(out io.Writer, rep *contract.NotifyReport) error {
	if rep == nil || !rep.Attempted || len(rep.Messages) == 0 {
		fmt.Fprintln(out, "no channel to test (enable a channel or pass --channel)")
		return nil
	}
	for _, m := range rep.Messages {
		for _, c := range m.Channels {
			switch c.Status {
			case "sent":
				fmt.Fprintf(out, "  ✔ %-8s sent\n", c.Channel)
			case "disabled":
				fmt.Fprintf(out, "  – %-8s disabled\n", c.Channel)
			default:
				msg := ""
				if c.Error != nil {
					msg = *c.Error
				}
				fmt.Fprintf(out, "  ✘ %-8s failed: %s\n", c.Channel, msg)
			}
		}
	}
	return nil
}

// notifyFromStdin reads a RunRequest from stdin (plugin IPC path).
func notifyFromStdin() (ipc.RunRequest, error) {
	var req ipc.RunRequest
	dec := json.NewDecoder(os.Stdin)
	if err := dec.Decode(&req); err != nil {
		return req, fmt.Errorf("decode RunRequest: %w", err)
	}
	return req, nil
}
