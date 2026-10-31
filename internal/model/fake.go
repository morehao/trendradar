package model

import (
	"context"
	"encoding/json"
	"regexp"
)

// Fake is an offline Provider: it echoes a deterministic JSON summary for every
// repo encoded in the prompt. It lets the whole summarize->render pipeline run
// without any network or API key.
type Fake struct{}

// NewFake returns an offline provider.
func NewFake() Provider { return &Fake{} }

func (f *Fake) Name() string { return "fake" }

var repoLineRe = regexp.MustCompile(`(?m)^REPO=(\S+)\tDESC=(.*)$`)

type fakeItem struct {
	FullName  string `json:"fullName"`
	SummaryZh string `json:"summaryZh"`
}

func (f *Fake) Complete(_ context.Context, _, user string) (Result, error) {
	var items []fakeItem
	for _, m := range repoLineRe.FindAllStringSubmatch(user, -1) {
		// Emit Chinese prose so the summary validator accepts offline output.
		items = append(items, fakeItem{FullName: m[1], SummaryZh: "[fake] 这是离线测试生成的中文示例摘要，仅用于验证流程。"})
	}
	b, err := json.Marshal(map[string]any{"summaries": items})
	if err != nil {
		return Result{}, err
	}
	return Result{Text: string(b)}, nil
}
