package summarize

import (
	"strings"

	"github.com/morehao/trendradar/internal/digest"
)

// SystemPrompt instructs the model to return strict JSON.
const SystemPrompt = `你是资深技术编辑。为给定的 GitHub 仓库各写一句简体中文摘要（不超过 60 字，客观、不夸大）。
只输出 JSON，格式：{"summaries":[{"fullName":"owner/repo","summaryZh":"..."}]}
不要输出任何额外文字、Markdown 代码块或解释。`

// buildUserPrompt encodes the batch in a machine-parseable form. The "REPO="
// lines are also what the offline Fake provider reads.
func buildUserPrompt(batch []digest.RepoSnapshot) string {
	var b strings.Builder
	b.WriteString("为下列仓库生成中文摘要：\n")
	for _, r := range batch {
		desc := strings.TrimSpace(r.Description)
		if desc == "" {
			desc = "(无描述)"
		}
		desc = strings.ReplaceAll(desc, "\n", " ")
		b.WriteString("REPO=" + r.FullName + "\tDESC=" + desc + "\n")
	}
	return b.String()
}
