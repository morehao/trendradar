package model

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
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
		desc := strings.TrimSpace(m[2])
		sum := "[fake] " + truncate(desc, 60)
		items = append(items, fakeItem{FullName: m[1], SummaryZh: sum})
	}
	b, err := json.Marshal(map[string]any{"summaries": items})
	if err != nil {
		return Result{}, err
	}
	return Result{Text: string(b)}, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
