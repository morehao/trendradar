package model

import (
	"context"
	"encoding/json"
	"testing"
)

func TestFakeProducesValidJSON(t *testing.T) {
	user := "为下列仓库生成中文摘要：\nREPO=acme/one\tDESC=first repo\nREPO=acme/two\tDESC=second repo\n"
	res, err := NewFake().Complete(context.Background(), SystemPromptForTest(), user)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Summaries []struct {
			FullName  string `json:"fullName"`
			SummaryZh string `json:"summaryZh"`
		} `json:"summaries"`
	}
	if err := json.Unmarshal([]byte(res.Text), &parsed); err != nil {
		t.Fatalf("fake output not json: %v (%s)", err, res.Text)
	}
	if len(parsed.Summaries) != 2 || parsed.Summaries[0].FullName != "acme/one" {
		t.Fatalf("unexpected summaries: %+v", parsed.Summaries)
	}
	if res.Degraded {
		t.Fatal("fake should not be degraded")
	}
}

func SystemPromptForTest() string { return "system" }
