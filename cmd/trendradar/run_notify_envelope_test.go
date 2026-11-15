package main

import (
	"encoding/json"
	"strings"
	"testing"

	contract "github.com/morehao/trendradar/contract"
)

// TestRunEnvelopeEmitsNotifySubtree guards the --json wire shape: a run that
// produced a notify report must serialize it under exactly one "notify" key,
// and a run without notify must omit it entirely (omitempty).
func TestRunEnvelopeEmitsNotifySubtree(t *testing.T) {
	rep := &contract.NotifyReport{
		Enabled:   true,
		Attempted: true,
		Messages: []contract.NotifyMessage{
			{
				Kind:          "trending",
				Period:        strPtr("daily"),
				BodyChars:     512,
				SummarySource: "model",
				Channels: []contract.NotifyChannelResult{
					{Channel: "wecom", Status: "sent", DurationMs: intPtr(118)},
					{Channel: "dingtalk", Status: "failed", Error: strPtr("errcode 310000")},
					{Channel: "lark", Status: "disabled"},
				},
			},
		},
	}
	out := runEnvelope{Notify: rep}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)

	// Exactly one notify key, and it carries the report fields.
	if n := strings.Count(s, `"notify"`); n != 1 {
		t.Fatalf("want exactly one \"notify\" key, got %d in %s", n, s)
	}
	for _, want := range []string{`"enabled":true`, `"attempted":true`, `"kind":"trending"`, `"status":"sent"`, `"status":"failed"`, `"status":"disabled"`} {
		if !strings.Contains(s, want) {
			t.Errorf("serialized envelope missing %s in %s", want, s)
		}
	}

	// No notify report -> the key must be omitted.
	b2, err := json.Marshal(runEnvelope{})
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if strings.Contains(string(b2), `"notify"`) {
		t.Fatalf("empty envelope should not emit \"notify\": %s", b2)
	}
}

func intPtr(i int) *int { return &i }
