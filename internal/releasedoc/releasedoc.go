// Package releasedoc owns the per-repo release document: one markdown file per
// repository, one section per ISO week, newest section first, split into
// zero-padded volumes (`<repo>-01.md`, `<repo>-02.md`, …) once a volume grows
// past a line budget.
//
// Why a separate package: the document is the single source of truth the panel
// renders verbatim (the panel never re-summarizes). That means its layout and
// its split/idempotency rules are a contract with the UI, not an implementation
// detail of the renderer, so they are testable without a git checkout or a
// model.
package releasedoc

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	contract "github.com/morehao/trendradar/contract"
)

// DefaultMaxLines is the write-before line budget. A volume is rolled over only
// when it *already* exceeds the budget before this write, so a single section is
// never split across volumes.
const DefaultMaxLines = 600

// MaxListedTags caps how many versions one week lists; the rest collapse into
// "其余 N 个". The weekly summary still covers every version.
const MaxListedTags = 10

// ErrNoDoc is returned when the repo has no release document yet.
var ErrNoDoc = errors.New("release document not found")

// ErrVolumeOutOfRange is returned when the requested volume does not exist.
var ErrVolumeOutOfRange = errors.New("release document volume out of range")

var repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// sectionRe matches a week heading, capturing its start and end dates.
var sectionRe = regexp.MustCompile(`^## (\d{4}-\d{2}-\d{2}) ~ (\d{4}-\d{2}-\d{2})\s*$`)

// ValidRepo reports whether repo is exactly "owner/name" with no traversal.
// The regex alone would accept "owner/..", so the dot segments are rejected
// explicitly.
func ValidRepo(repo string) bool {
	repo = strings.TrimSpace(repo)
	if !repoRe.MatchString(repo) {
		return false
	}
	for _, part := range strings.Split(repo, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

// Rel returns a volume's path relative to the release document root:
// <owner>/<repo>-01.md, <owner>/<repo>-02.md, … The first volume carries an
// explicit `-01` too, so every volume sorts and reads the same way (and a repo
// whose history was split never looks like it has one unnumbered document plus
// numbered ones).
//
// The contract's SubscriptionVolume.file uses this form so a client can show a
// portable path whether serve was pointed at <repo>/digests or at an absolute
// directory.
func Rel(repo string, index int) string {
	owner, _, _ := strings.Cut(repo, "/")
	return path.Join(owner, volumeName(repo, index))
}

// Path joins the document root with a volume's relative path
// (<root>/<owner>/<repo>-0N.md). root may be repository-relative (what the
// writer uses) or absolute (what serve uses).
func Path(root, repo string, index int) string {
	return path.Join(root, Rel(repo, index))
}

// volumeName is the file's base name for one volume: <repo>-01.md, <repo>-02.md…
func volumeName(repo string, index int) string {
	_, name, _ := strings.Cut(repo, "/")
	if index < 1 {
		index = 1
	}
	return fmt.Sprintf("%s-%02d.md", name, index)
}

// Item is one version listed in a week section.
type Item struct {
	Tag  string
	URL  string
	Date string // YYYY-MM-DD, empty when unknown
}

// Section is one week of a repo's release history.
type Section struct {
	Repo      string
	WeekStart string
	WeekEnd   string
	SummaryZh string
	Degraded  bool
	Items     []Item
	// MaxListed overrides MaxListedTags when > 0.
	MaxListed int
}

// Heading is the exact line that identifies the section inside a volume.
func (s Section) Heading() string {
	return "## " + s.WeekStart + " ~ " + s.WeekEnd
}

// Date is the section's period end, which is what the document's "最近更新"
// line and the on-disk weekly digest both use.
func (s Section) Date() string { return s.WeekEnd }

// Render renders the section markdown. It starts with the heading and ends with
// a blank line, so appending it before an existing heading keeps one blank line
// of separation.
func (s Section) Render() string {
	var b strings.Builder
	b.WriteString(s.Heading())
	b.WriteString("\n\n")
	limit := s.MaxListed
	if limit <= 0 {
		limit = MaxListedTags
	}
	listed := s.Items
	rest := 0
	if len(listed) > limit {
		rest = len(listed) - limit
		listed = listed[:limit]
	}
	for _, it := range listed {
		tag := escapeLinkText(it.Tag)
		if it.URL != "" {
			fmt.Fprintf(&b, "- [%s](%s)", tag, it.URL)
		} else {
			fmt.Fprintf(&b, "- %s", tag)
		}
		if d := shortDate(it.Date); d != "" {
			fmt.Fprintf(&b, " · %s", d)
		}
		b.WriteString("\n")
	}
	if rest > 0 {
		fmt.Fprintf(&b, "- 其余 %d 个\n", rest)
	}
	summary := strings.TrimSpace(s.SummaryZh)
	if summary != "" {
		b.WriteString("\n")
		if s.Degraded {
			b.WriteString("> ⚠️ 本周模型摘要不可用，以下为原文摘录。\n\n")
		}
		b.WriteString(summary)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}

// shortDate renders MM-DD, the form the prototype and the section headings use.
func shortDate(date string) string {
	if len(date) < 10 {
		return ""
	}
	return date[5:10]
}

// escapeLinkText keeps a tag from breaking the markdown link it lives in.
func escapeLinkText(s string) string {
	return strings.NewReplacer("[", "\\[", "]", "\\]", "(", "\\(", ")", "\\)").Replace(s)
}

// header renders the volume header. Volume 1 carries only the repo title and the
// freshness line; later volumes add a link to the previous volume so a reader
// can walk the whole history.
func header(repo string, index int, prevFile, updatedAt string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", repo)
	if index > 1 && prevFile != "" {
		fmt.Fprintf(&b, "> 上一册：[第 %d 册](%s)\n", index-1, prevFile)
	}
	fmt.Fprintf(&b, "> 发版记录 · 每周一次 · 最近更新 %s\n\n", updatedAt)
	return b.String()
}

// Index lists the volumes that exist for repo, oldest index first. Volume files
// are contiguous by construction (the writer only ever appends the next index),
// so the scan stops at the first missing file. A repo with no document yields an
// empty list (not an error): "no document yet" is a normal state for a freshly
// added subscription.
func Index(repoRoot, root, repo string) ([]contract.SubscriptionVolume, error) {
	if !ValidRepo(repo) {
		return nil, fmt.Errorf("invalid repo %q", repo)
	}
	var out []contract.SubscriptionVolume
	for index := 1; index <= 99; index++ {
		rel := Path(root, repo, index)
		data, err := os.ReadFile(diskPath(repoRoot, rel))
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return nil, err
		}
		content := string(data)
		from, to := spanOf(content)
		lines := lineCount(content)
		v := contract.SubscriptionVolume{Index: index, File: Rel(repo, index), Lines: &lines}
		if from != "" {
			v.From = &from
		}
		if to != "" {
			v.To = &to
		}
		out = append(out, v)
	}
	return out, nil
}

// Read returns one volume's markdown plus the volume index. volume <= 0 means
// "the newest volume". The returned markdown is byte-identical to the file.
func Read(repoRoot, root, repo string, volume int) (contract.SubscriptionDoc, error) {
	vols, err := Index(repoRoot, root, repo)
	if err != nil {
		return contract.SubscriptionDoc{}, err
	}
	if len(vols) == 0 {
		return contract.SubscriptionDoc{}, ErrNoDoc
	}
	index := volume
	if index <= 0 {
		index = vols[len(vols)-1].Index
	}
	if index < 1 || index > vols[len(vols)-1].Index {
		return contract.SubscriptionDoc{}, ErrVolumeOutOfRange
	}
	rel := Path(root, repo, index)
	data, err := os.ReadFile(diskPath(repoRoot, rel))
	if os.IsNotExist(err) {
		return contract.SubscriptionDoc{}, ErrVolumeOutOfRange
	}
	if err != nil {
		return contract.SubscriptionDoc{}, err
	}
	doc := contract.SubscriptionDoc{FullName: repo, Volume: index, Volumes: vols, Markdown: string(data)}
	if updated := updatedOf(string(data)); updated != "" {
		doc.UpdatedAt = &updated
	}
	return doc, nil
}

// Latest returns the newest volume's updated date, if a document exists.
func Latest(repoRoot, root, repo string) (string, bool) {
	vols, err := Index(repoRoot, root, repo)
	if err != nil || len(vols) == 0 {
		return "", false
	}
	last := vols[len(vols)-1]
	if last.To != nil && *last.To != "" {
		return *last.To, true
	}
	return "", false
}

// Plan renders sec against the repo's current volume and returns the
// repository-relative path and full file content to write. need is false when
// this exact week is already recorded anywhere in the document, which is what
// makes a repeated run a no-op (A4).
//
// The roll-over decision looks only at the current volume *before* writing: when
// it already exceeds maxLines a new volume starts, otherwise this section lands
// whole in the current one even if it pushes the file past the budget. A week is
// therefore never split across two volumes and never lost (A6).
//
// A missing document starts volume 1. A document whose first line is not
// "# owner/repo" is refused with an error instead of being overwritten: a human
// edited it, and silently clobbering their edit is worse than skipping this repo.
func Plan(repoRoot, root, repo string, sec Section, maxLines int) (file string, data []byte, need bool, err error) {
	if !ValidRepo(repo) {
		return "", nil, false, fmt.Errorf("invalid repo %q", repo)
	}
	if sec.WeekStart == "" || sec.WeekEnd == "" {
		return "", nil, false, fmt.Errorf("release section needs week bounds")
	}
	if maxLines <= 0 {
		maxLines = DefaultMaxLines
	}
	vols, err := Index(repoRoot, root, repo)
	if err != nil {
		return "", nil, false, err
	}
	if len(vols) == 0 {
		content := header(repo, 1, "", sec.Date()) + sec.Render()
		return Path(root, repo, 1), []byte(content), true, nil
	}

	heading := sec.Heading()
	for _, v := range vols {
		rel := Path(root, repo, v.Index)
		existing, err := os.ReadFile(diskPath(repoRoot, rel))
		if err != nil {
			return "", nil, false, err
		}
		if hasHeading(string(existing), heading) {
			return rel, existing, false, nil
		}
	}

	current := vols[len(vols)-1]
	currentRel := Path(root, repo, current.Index)
	existing, err := os.ReadFile(diskPath(repoRoot, currentRel))
	if err != nil {
		return "", nil, false, err
	}
	content := string(existing)
	first := firstLine(content)
	if first != "# "+repo {
		return "", nil, false, fmt.Errorf("refusing to write %s: first line is %q, want %q", currentRel, first, "# "+repo)
	}
	if lineCount(content) > maxLines {
		index := current.Index + 1
		prev := volumeName(repo, current.Index)
		body := header(repo, index, prev, sec.Date()) + sec.Render()
		return Path(root, repo, index), []byte(body), true, nil
	}
	return currentRel, []byte(prepend(content, sec)), true, nil
}

// prepend inserts sec before the first section heading and refreshes the
// freshness line. No existing bytes other than that one line are rewritten, so
// hand-written prose below the heading survives.
func prepend(content string, sec Section) string {
	trimmed := strings.TrimRight(content, "\n")
	lines := strings.Split(trimmed, "\n")
	insertAt := len(lines)
	for i, line := range lines {
		if strings.HasPrefix(line, "## ") {
			insertAt = i
			break
		}
	}
	// Keep the "最近更新" line in step with the section we are adding.
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "> 发版记录") {
			lines[i] = "> 发版记录 · 每周一次 · 最近更新 " + sec.Date()
			break
		}
	}
	// sec.Render ends with a blank line; drop every trailing "" so the preserved
	// blank line before the next heading is the only separator.
	sectionLines := strings.Split(sec.Render(), "\n")
	for len(sectionLines) > 0 && sectionLines[len(sectionLines)-1] == "" {
		sectionLines = sectionLines[:len(sectionLines)-1]
	}
	out := make([]string, 0, len(lines)+len(sectionLines)+1)
	out = append(out, lines[:insertAt]...)
	out = append(out, sectionLines...)
	if insertAt < len(lines) {
		out = append(out, "")
		out = append(out, lines[insertAt:]...)
	}
	return strings.Join(out, "\n") + "\n"
}

// hasHeading reports whether content already contains the exact week heading.
func hasHeading(content, heading string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == heading {
			return true
		}
	}
	return false
}

// firstLine returns the first non-empty line, trimmed.
func firstLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	return ""
}

// updatedOf extracts the date from the freshness line.
func updatedOf(content string) string {
	for _, line := range strings.Split(content, "\n") {
		s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), ">"))
		if strings.HasPrefix(s, "发版记录") {
			if i := strings.LastIndex(s, " "); i >= 0 {
				return strings.TrimSpace(s[i+1:])
			}
		}
	}
	return ""
}

// spanOf returns the oldest start and newest end date across a volume's
// sections.
func spanOf(content string) (from, to string) {
	for _, line := range strings.Split(content, "\n") {
		m := sectionRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		if to == "" || m[2] > to {
			to = m[2]
		}
		if from == "" || m[1] < from {
			from = m[1]
		}
	}
	return from, to
}

// lineCount counts lines the way an editor shows them: a trailing newline does
// not add an extra line.
func lineCount(content string) int {
	if content == "" {
		return 0
	}
	return len(strings.Split(strings.TrimSuffix(content, "\n"), "\n"))
}

// diskPath joins a repository-relative path to the checkout root and verifies
// the result stays inside the checkout. repo is already validated, so this is
// defence in depth against a future caller passing an unvalidated string.
func diskPath(repoRoot, rel string) string {
	clean := path.Clean(rel)
	if path.IsAbs(clean) {
		// serve resolves the document root to an absolute directory; the
		// traversal guard already happened on the repo string.
		return filepath.FromSlash(clean)
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return filepath.Join(repoRoot, "__rejected__")
	}
	return filepath.Join(repoRoot, filepath.FromSlash(clean))
}

// ParseVolume parses a client-supplied volume number; empty means newest.
func ParseVolume(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("volume must be a positive integer, got %q", raw)
	}
	return n, nil
}
