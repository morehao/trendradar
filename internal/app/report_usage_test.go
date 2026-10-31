package app

import (
	"encoding/json"
	"testing"

	"github.com/morehao/trendradar/internal/releasewatch"
)

type releasewatchReport = releasewatch.Report

// TestReleaseReportUsageNeverDisappears guards the contract promise that
// ReleasesReport.usage is always present: a consumer must be able to tell
// "nothing spent" from "the field is missing".
func TestReleaseReportUsageNeverDisappears(t *testing.T) {
	rep := fromReleaseWatch(releasewatchReportZero())
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	usage, ok := m["usage"].(map[string]any)
	if !ok {
		t.Fatalf("usage missing in %s", b)
	}
	if usage["totalTokens"].(float64) != 0 {
		t.Fatalf("totalTokens = %v, want 0", usage["totalTokens"])
	}
	if usage["usageKnown"].(bool) {
		t.Fatalf("usageKnown = true, want false")
	}
	// The stage must still be distinguishable from "did not run".
	if _, ok := m["enabled"]; !ok {
		t.Fatalf("enabled missing in %s", b)
	}
}

// TestReleaseReportUsageWithCost covers the normal path.
func TestReleaseReportUsageWithCost(t *testing.T) {
	r := releasewatchReportZero()
	r.Enabled = true
	r.Repos = 2
	r.NewReleases = 4
	r.Summarized = 4
	r.TotalTokens = 3864
	r.PromptTokens = 2918
	r.CompletionTokens = 946
	r.UsageKnown = true
	r.Calls = 2
	r.DurationMs = 16851

	rep := fromReleaseWatch(r)
	b, _ := json.Marshal(rep)
	var m map[string]any
	_ = json.Unmarshal(b, &m)

	usage := m["usage"].(map[string]any)
	if usage["totalTokens"].(float64) != 3864 {
		t.Fatalf("totalTokens = %v", usage["totalTokens"])
	}
	if !usage["usageKnown"].(bool) {
		t.Fatal("usageKnown = false")
	}
	if usage["calls"].(float64) != 2 {
		t.Fatalf("calls = %v", usage["calls"])
	}
	if m["newReleases"].(float64) != 4 {
		t.Fatalf("newReleases = %v", m["newReleases"])
	}
}

func releasewatchReportZero() releasewatchReport {
	return releasewatchReport{}
}
