package summarize

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/morehao/trendradar/internal/digest"
)

// ErrBadOutput marks model output that could not be decoded into valid summaries.
var ErrBadOutput = errors.New("summarize: invalid model output")

type response struct {
	Summaries []struct {
		FullName  string `json:"fullName"`
		SummaryZh string `json:"summaryZh"`
	} `json:"summaries"`
}

// parseResponse decodes the model output and keeps only summaries whose
// fullName came from the input batch.
func parseResponse(raw string, batch []digest.RepoSnapshot) (map[string]string, error) {
	raw = stripFence(strings.TrimSpace(raw))
	var resp response
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadOutput, err)
	}
	allowed := make(map[string]bool, len(batch))
	for _, r := range batch {
		allowed[r.FullName] = true
	}
	out := make(map[string]string, len(resp.Summaries))
	for _, s := range resp.Summaries {
		sum := strings.TrimSpace(s.SummaryZh)
		if allowed[s.FullName] && sum != "" {
			out[s.FullName] = sum
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no valid summaries for input batch", ErrBadOutput)
	}
	return out, nil
}

// stripFence removes a surrounding ```json ... ``` code fence if present.
func stripFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimPrefix(s, "json")
	s = strings.TrimPrefix(s, "\n")
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}
