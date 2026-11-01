package releasedoc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDoc(t *testing.T, repoRoot, rel, content string) {
	t.Helper()
	p := filepath.Join(repoRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func section(weekStart, weekEnd string, tags ...string) Section {
	items := make([]Item, 0, len(tags))
	for _, tag := range tags {
		items = append(items, Item{Tag: tag, URL: "https://github.com/o/r/releases/tag/" + tag, Date: weekEnd})
	}
	return Section{
		Repo:      "o/r",
		WeekStart: weekStart,
		WeekEnd:   weekEnd,
		SummaryZh: "本周发布 " + strings.Join(tags, "、") + "，修复若干问题。",
		Items:     items,
	}
}

func TestPlanStartsVolumeOne(t *testing.T) {
	root := t.TempDir()
	file, data, need, err := Plan(root, "digests/releases", "o/r", section("2026-10-26", "2026-11-01", "v2", "v1"), 600)
	if err != nil {
		t.Fatal(err)
	}
	if !need {
		t.Fatal("first write must be needed")
	}
	if file != "digests/releases/o/r-01.md" {
		t.Fatalf("file=%q", file)
	}
	text := string(data)
	if !strings.HasPrefix(text, "# o/r\n\n") {
		t.Fatalf("missing title:\n%s", text)
	}
	if !strings.Contains(text, "> 发版记录 · 每周一次 · 最近更新 2026-11-01") {
		t.Fatalf("missing freshness line:\n%s", text)
	}
	if !strings.Contains(text, "## 2026-10-26 ~ 2026-11-01") {
		t.Fatalf("missing week heading:\n%s", text)
	}
	if strings.Index(text, "- [v2]") > strings.Index(text, "- [v1]") {
		t.Fatalf("items must be newest first:\n%s", text)
	}
}

// A4/A5: writing the same week again is a no-op, so a duplicate run cannot
// append a second section or change the file's bytes.
func TestPlanIsIdempotentForSameWeek(t *testing.T) {
	root := t.TempDir()
	sec := section("2026-10-26", "2026-11-01", "v2")
	file, data, need, err := Plan(root, "digests/releases", "o/r", sec, 600)
	if err != nil || !need {
		t.Fatalf("plan: need=%v err=%v", need, err)
	}
	writeDoc(t, root, file, string(data))

	_, again, need, err := Plan(root, "digests/releases", "o/r", sec, 600)
	if err != nil {
		t.Fatal(err)
	}
	if need {
		t.Fatal("second plan for the same week must not write")
	}
	if string(again) != string(data) {
		t.Fatalf("plan returned different bytes for an unchanged document")
	}
}

func TestPlanPrependsNewestFirst(t *testing.T) {
	root := t.TempDir()
	week1 := section("2026-10-19", "2026-10-25", "v1")
	file, data, _, err := Plan(root, "digests/releases", "o/r", week1, 600)
	if err != nil {
		t.Fatal(err)
	}
	writeDoc(t, root, file, string(data))

	week2 := section("2026-10-26", "2026-11-01", "v2")
	file, data, need, err := Plan(root, "digests/releases", "o/r", week2, 600)
	if err != nil || !need {
		t.Fatalf("plan: need=%v err=%v", need, err)
	}
	text := string(data)
	first := strings.Index(text, "## 2026-10-26 ~ 2026-11-01")
	second := strings.Index(text, "## 2026-10-19 ~ 2026-10-25")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("newest week must be first:\n%s", text)
	}
	if !strings.Contains(text, "最近更新 2026-11-01") {
		t.Fatalf("freshness line not refreshed:\n%s", text)
	}
	// Both weeks live in the same volume: the budget was never exceeded.
	if _, err := os.Stat(filepath.Join(root, "digests", "releases", "o", "r-02.md")); !os.IsNotExist(err) {
		t.Fatalf("unexpected volume 2: %v", err)
	}
}

// A6: the roll-over decision looks only at the current volume *before* writing.
// A 599-line volume absorbs a 20-line week whole (no split), and only the next
// week starts -02.md.
func TestPlanRollsVolumeOnlyAfterExceedingBudget(t *testing.T) {
	root := t.TempDir()
	rel := "digests/releases/o/r-01.md"
	var b strings.Builder
	b.WriteString("# o/r\n\n> 发版记录 · 每周一次 · 最近更新 2026-10-25\n\n")
	for lineCount(b.String()) < 599 {
		b.WriteString("filler line\n")
	}
	writeDoc(t, root, rel, b.String())
	if got := lineCount(b.String()); got != 599 {
		t.Fatalf("fixture lines=%d, want 599", got)
	}

	week1 := section("2026-10-26", "2026-11-01", "v2", "v1")
	week1.SummaryZh = strings.Repeat("总结内容。", 8)
	file, data, need, err := Plan(root, "digests/releases", "o/r", week1, 600)
	if err != nil || !need {
		t.Fatalf("plan: need=%v err=%v", need, err)
	}
	if file != rel {
		t.Fatalf("file=%q, want the current volume", file)
	}
	if !strings.Contains(string(data), "## 2026-10-26 ~ 2026-11-01") {
		t.Fatalf("week section missing:\n%s", data)
	}
	writeDoc(t, root, file, string(data))
	if got := lineCount(string(data)); got <= 600 {
		t.Fatalf("volume lines=%d, want > 600 so the next write rolls", got)
	}
	if _, err := os.Stat(filepath.Join(root, "digests", "releases", "o", "r-02.md")); !os.IsNotExist(err) {
		t.Fatalf("volume must not split mid-write: %v", err)
	}

	week2 := section("2026-11-02", "2026-11-08", "v3")
	file2, data2, need2, err := Plan(root, "digests/releases", "o/r", week2, 600)
	if err != nil || !need2 {
		t.Fatalf("second plan: need=%v err=%v", need2, err)
	}
	if file2 != "digests/releases/o/r-02.md" {
		t.Fatalf("file=%q, want the new volume", file2)
	}
	text2 := string(data2)
	if !strings.HasPrefix(text2, "# o/r\n") {
		t.Fatalf("new volume must start with the title:\n%s", text2)
	}
	if !strings.Contains(text2, "[第 1 册](r-01.md)") {
		t.Fatalf("new volume must link the previous one:\n%s", text2)
	}
	if strings.Contains(text2, "2026-10-26 ~ 2026-11-01") {
		t.Fatalf("new volume must not repeat older weeks:\n%s", text2)
	}
}

// A hand-edited document must be refused, never overwritten: the title line is
// the integrity check.
func TestPlanRefusesEditedTitle(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "digests/releases/o/r-01.md", "# something else\n\n## 2026-10-19 ~ 2026-10-25\n\nold\n")
	_, _, _, err := Plan(root, "digests/releases", "o/r", section("2026-10-26", "2026-11-01", "v2"), 600)
	if err == nil {
		t.Fatal("want an error for a mismatched title")
	}
	if !strings.Contains(err.Error(), "refusing to write") {
		t.Fatalf("err=%v", err)
	}
}

func TestReadVolumesAndIndex(t *testing.T) {
	root := t.TempDir()
	week1 := section("2026-10-19", "2026-10-25", "v1")
	file, data, _, err := Plan(root, "digests/releases", "o/r", week1, 600)
	if err != nil {
		t.Fatal(err)
	}
	writeDoc(t, root, file, string(data))
	writeDoc(t, root, "digests/releases/o/r-02.md", "# o/r\n\n> 上一册：[第 1 册](r-01.md)\n> 发版记录 · 每周一次 · 最近更新 2026-11-01\n\n## 2026-10-26 ~ 2026-11-01\n\nnew\n")

	vols, err := Index(root, "digests/releases", "o/r")
	if err != nil {
		t.Fatal(err)
	}
	if len(vols) != 2 {
		t.Fatalf("volumes=%d, want 2", len(vols))
	}
	if vols[0].File != "o/r-01.md" || vols[1].File != "o/r-02.md" {
		t.Fatalf("files=%q/%q", vols[0].File, vols[1].File)
	}

	doc, err := Read(root, "digests/releases", "o/r", 0)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Volume != 2 {
		t.Fatalf("default volume=%d, want newest (2)", doc.Volume)
	}
	if doc.UpdatedAt == nil || *doc.UpdatedAt != "2026-11-01" {
		t.Fatalf("updatedAt=%v", doc.UpdatedAt)
	}
	if _, err := Read(root, "digests/releases", "o/r", 3); err != ErrVolumeOutOfRange {
		t.Fatalf("err=%v, want out-of-range", err)
	}
	if _, err := Read(root, "digests/releases", "o/missing", 0); err != ErrNoDoc {
		t.Fatalf("err=%v, want no-document", err)
	}
}

func TestSectionCapsListedTags(t *testing.T) {
	tags := make([]string, 0, 14)
	for i := 14; i >= 1; i-- {
		tags = append(tags, "v"+strings.Repeat("x", 3)+string(rune('a'+i-1)))
	}
	sec := section("2026-10-26", "2026-11-01", tags...)
	text := sec.Render()
	if strings.Count(text, "- [") != MaxListedTags {
		t.Fatalf("listed=%d, want %d:\n%s", strings.Count(text, "- ["), MaxListedTags, text)
	}
	if !strings.Contains(text, "- 其余 4 个") {
		t.Fatalf("missing remainder note:\n%s", text)
	}
}

func TestValidRepoRejectsTraversal(t *testing.T) {
	for _, repo := range []string{"o/r", "a-b/c.d", "a_b/c-d"} {
		if !ValidRepo(repo) {
			t.Fatalf("%q should be valid", repo)
		}
	}
	for _, repo := range []string{"o", "o/", "/r", "o/r/s", "o/..", "../r", "o/./r", "o/r x"} {
		if ValidRepo(repo) {
			t.Fatalf("%q should be rejected", repo)
		}
	}
}

func TestParseVolume(t *testing.T) {
	if v, err := ParseVolume(""); err != nil || v != 0 {
		t.Fatalf("empty volume = %d/%v", v, err)
	}
	if v, err := ParseVolume(" 3 "); err != nil || v != 3 {
		t.Fatalf("volume = %d/%v", v, err)
	}
	for _, raw := range []string{"0", "-1", "x", "1.5"} {
		if _, err := ParseVolume(raw); err == nil {
			t.Fatalf("%q should be rejected", raw)
		}
	}
}

// A document whose week is present in an older volume must still be recognized,
// otherwise a roll-over could duplicate a week into the new volume.
func TestPlanSeesWeekInOlderVolume(t *testing.T) {
	root := t.TempDir()
	week1 := section("2026-10-19", "2026-10-25", "v1")
	file, data, _, err := Plan(root, "digests/releases", "o/r", week1, 600)
	if err != nil {
		t.Fatal(err)
	}
	writeDoc(t, root, file, string(data))
	// Simulate a rolled document that already contains the next week.
	writeDoc(t, root, "digests/releases/o/r-02.md", "# o/r\n\n> 上一册：[第 1 册](r-01.md)\n> 发版记录 · 每周一次 · 最近更新 2026-11-01\n\n## 2026-10-26 ~ 2026-11-01\n\nnew\n")

	_, _, need, err := Plan(root, "digests/releases", "o/r", section("2026-10-26", "2026-11-01", "v2"), 600)
	if err != nil {
		t.Fatal(err)
	}
	if need {
		t.Fatal("week present in the newest volume must not be written again")
	}
}
