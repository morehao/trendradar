package releasewatch

import (
	"strings"
	"testing"

	"github.com/morehao/trendradar/internal/fetch"
)

func TestSanitizePreservesStructure(t *testing.T) {
	in := "本周要点一句话。\n\n**主要变更**\n- 新增 A 功能\n- 修复 B 问题\n\n**升级建议**\n- 建议升级"
	got := sanitize(in)
	wantLines := []string{
		"本周要点一句话。",
		"",
		"**主要变更**",
		"- 新增 A 功能",
		"- 修复 B 问题",
		"",
		"**升级建议**",
		"- 建议升级",
	}
	if got != strings.Join(wantLines, "\n") {
		t.Fatalf("sanitize structure not preserved.\n got=%q\nwant=%q", got, strings.Join(wantLines, "\n"))
	}
}

func TestSanitizeStripsLinksHTMLURLs(t *testing.T) {
	in := "修复 [见 PR](https://example.com/pr/1) 与 <b>标签</b>，详情 https://x.y/z 。"
	got := sanitize(in)
	if strings.Contains(got, "http") || strings.Contains(got, "<b>") || strings.Contains(got, "](") {
		t.Fatalf("sanitize leaked unsafe content: %q", got)
	}
	if !strings.Contains(got, "修复") {
		t.Fatalf("sanitize dropped real text: %q", got)
	}
}

func TestSanitizeStripsRogueHeadings(t *testing.T) {
	// A model-emitted ATX heading must not survive: the release document splits
	// sections on `## ` lines, so a stray heading could corrupt parsing.
	for _, in := range []string{"## 2026-01-01 ~ 2026-01-07\n正文", "# 标题\n正文"} {
		got := sanitize(in)
		for _, line := range strings.Split(got, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				t.Fatalf("sanitize kept a raw heading line %q from %q", line, in)
			}
		}
	}
}

func TestSanitizeCollapsesBlankRuns(t *testing.T) {
	got := sanitize("A\n\n\n\n\nB")
	if got != "A\n\nB" {
		t.Fatalf("blank runs not collapsed: %q", got)
	}
}

func TestSanitizeNormalizesBullets(t *testing.T) {
	got := sanitize("• 第一条\n· 第二条")
	if got != "- 第一条\n- 第二条" {
		t.Fatalf("bullets not normalized: %q", got)
	}
}

func TestDegradeSummaryKeepsNotesOnOwnLines(t *testing.T) {
	cands := []candidate{
		{rel: fetch.Release{TagName: "v1.0.0", Name: "v1.0.0", Body: "修复了 A"}},
		{rel: fetch.Release{TagName: "v1.0.1", Name: "v1.0.1", Body: "新增了 B"}},
	}
	got := degradeSummary(cands)
	if !strings.Contains(got, "**v1.0.0**\n修复了 A") {
		t.Fatalf("degrade summary should keep tag label on its own line: %q", got)
	}
	if !strings.Contains(got, "\n\n") {
		t.Fatalf("degrade summary should separate versions with a blank line: %q", got)
	}
}
