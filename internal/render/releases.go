package render

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/morehao/trendradar/internal/releasedigest"
)

// releasesSubdir normalizes the configured releases sub-directory so a bad
// config can never escape the digest root.
func releasesSubdir(sub string) string {
	s := path.Clean(strings.ReplaceAll(strings.TrimSpace(sub), "\\", "/"))
	if s == "" || s == "." || path.IsAbs(s) || s == ".." || strings.HasPrefix(s, "../") {
		return "releases"
	}
	return s
}

func releasePeriod(period string) string {
	switch period {
	case PeriodWeekly, PeriodMonthly:
		return period
	default:
		return PeriodDaily
	}
}

// ReleaseDir returns the on-disk directory for one period's digests. Weekly
// digests live under a leading underscore so the directory cannot collide with a
// subscribed owner named "weekly": releases/ holds one directory per owner plus
// the aggregated periods, and an owner called "weekly" would otherwise share a
// directory with the week rollups.
func ReleaseDir(period string) string {
	if releasePeriod(period) == PeriodWeekly {
		return "_" + PeriodWeekly
	}
	return releasePeriod(period)
}

// ReleaseDirs returns the directories a reader must scan for one period, newest
// convention first. Weekly digests written by 0.6.x live in "weekly"; keeping it
// in the read path means upgrading does not hide already-committed history.
func ReleaseDirs(period string) []string {
	p := releasePeriod(period)
	dir := ReleaseDir(p)
	if dir == p {
		return []string{p}
	}
	return []string{dir, p}
}

// ReleaseRoot returns the repository-relative releases root inside outDir.
func ReleaseRoot(outDir, subdir string) string {
	return path.Join(OutputRoot(outDir), releasesSubdir(subdir))
}

// BuildReleases renders one release digest plus the releases index. Artifacts
// live at <outDir>/<subdir>/<ReleaseDir(period)>/<bucket>/<name>.{md,json}; index
// at <outDir>/<subdir>/index.md. Paths are repository-relative so the same
// g.AddAll(ctx, outDir) that stages trending also stages these.
func BuildReleases(d releasedigest.ReleaseDigest, history []releasedigest.ReleaseDigest, limits map[string]int, outDir, subdir string) ([]File, error) {
	root := ReleaseRoot(outDir, subdir)
	period := releasePeriod(d.Period)
	dir := path.Join(root, ReleaseDir(period), BucketOf(period, d.Date))
	name := NameOf(period, d.Date)
	js, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, err
	}
	js = append(js, '\n')
	return []File{
		{Path: path.Join(dir, name+".md"), Data: ReleaseMarkdown(d)},
		{Path: path.Join(dir, name+".json"), Data: js},
		{Path: path.Join(root, "index.md"), Data: ReleaseIndex(history, limits, root)},
	}, nil
}

// ReleaseMarkdown renders one period's release digest.
func ReleaseMarkdown(d releasedigest.ReleaseDigest) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "date: %s\nperiod: %s\ncount: %d\ndegraded: %t\n", d.Date, d.Period, d.Count, d.Degraded)
	fmt.Fprintf(&b, "contentHash: %s\n", d.ContentHash)
	coreVersion := ""
	if d.CoreVersion != nil {
		coreVersion = *d.CoreVersion
	}
	fmt.Fprintf(&b, "coreVersion: %s\n", coreVersion)
	gen := ""
	if d.GeneratedAt != nil {
		gen = d.GeneratedAt.UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(&b, "generatedAt: %s\n---\n\n", gen)
	fmt.Fprintf(&b, "# %s 发版摘要\n\n", d.Date)
	if d.Degraded {
		b.WriteString("> ⚠️ 部分版本未获取到有效变更说明，已降级为原文摘要（summarySource=original）。\n\n")
	}
	if len(d.Repos) == 0 {
		b.WriteString("_本次没有订阅仓库的新版本。_\n")
	}
	for _, r := range d.Repos {
		period := ""
		if r.PeriodStart != nil && r.PeriodEnd != nil {
			period = " · " + *r.PeriodStart + " ~ " + *r.PeriodEnd
		}
		fmt.Fprintf(&b, "## [%s](%s) · %s%s\n\n", r.FullName, r.URL, r.LatestTag, period)
		if r.SummaryZh != nil && *r.SummaryZh != "" {
			fmt.Fprintf(&b, "%s\n\n", *r.SummaryZh)
		}
		for _, it := range r.Releases {
			date := ""
			if it.PublishedAt != nil {
				date = it.PublishedAt.Format("2006-01-02")
			}
			fmt.Fprintf(&b, "- **%s**%s", it.Tag, dateSuffix(date))
			// Schema 2 leaves per-item summaries empty (the summary is one per
			// repo-week); old documents keep their per-item prose readable.
			if it.SummaryZh != nil && *it.SummaryZh != "" {
				fmt.Fprintf(&b, " — %s", *it.SummaryZh)
			}
			b.WriteString("\n")
			if it.URL != "" {
				fmt.Fprintf(&b, "  链接: %s\n", it.URL)
			}
		}
		b.WriteString("\n")
	}
	return []byte(b.String())
}

func dateSuffix(date string) string {
	if date == "" {
		return ""
	}
	return " (" + date + ")"
}

// ReleaseIndex renders the releases index grouped by period, newest first.
func ReleaseIndex(digests []releasedigest.ReleaseDigest, limits map[string]int, root string) []byte {
	byPeriod := map[string][]releasedigest.ReleaseDigest{}
	for _, d := range digests {
		p := releasePeriod(d.Period)
		byPeriod[p] = append(byPeriod[p], d)
	}
	var b strings.Builder
	b.WriteString("# 发版索引\n")
	for _, period := range []string{PeriodDaily, PeriodWeekly, PeriodMonthly} {
		ds := byPeriod[period]
		if len(ds) == 0 {
			continue
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i].Date > ds[j].Date })
		if limit := limits[period]; limit > 0 && len(ds) > limit {
			ds = ds[:limit]
		}
		fmt.Fprintf(&b, "\n## %s\n\n", periodTitle(period))
		b.WriteString("| 日期 | 数量 | 降级 | 摘要 |\n|---|---|---|---|\n")
		for _, d := range ds {
			name := NameOf(period, d.Date)
			link := path.Join(root, ReleaseDir(period), BucketOf(period, d.Date), name+".md")
			fmt.Fprintf(&b, "| %s | %d | %t | [%s](%s) |\n", d.Date, d.Count, d.Degraded, name, link)
		}
	}
	return []byte(b.String())
}

// ListReleaseDigests scans <root>/<subdir>/<period>/<bucket>/ for release
// digests, newest first per period, capped by limits. root is the repository
// path joined with output.dir (NOT the repository root alone).
func ListReleaseDigests(root, subdir string, limits map[string]int) ([]releasedigest.ReleaseDigest, error) {
	base := filepath.Join(root, filepath.FromSlash(releasesSubdir(subdir)))
	var out []releasedigest.ReleaseDigest
	for _, period := range []string{PeriodDaily, PeriodWeekly, PeriodMonthly} {
		ds, err := listReleasePeriodDirs(base, period, limits[period])
		if err != nil {
			return nil, err
		}
		out = append(out, ds...)
	}
	return out, nil
}

// listReleasePeriodDirs scans every directory ReleaseDirs reports for the period
// (current convention plus the pre-0.7 weekly/ location) and keeps one document
// per (period, date). The prefixed location wins when both exist: it is what this
// version writes, so a half-migrated repo lists the fresh copy.
func listReleasePeriodDirs(base, period string, limit int) ([]releasedigest.ReleaseDigest, error) {
	re := periodFileRe(period)
	type ref struct{ name, path string }
	var refs []ref
	seen := map[string]bool{}
	for _, dirName := range ReleaseDirs(period) {
		periodDir := filepath.Join(base, dirName)
		buckets, err := os.ReadDir(periodDir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, b := range buckets {
			if !b.IsDir() {
				continue
			}
			dir := filepath.Join(periodDir, b.Name())
			files, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, f := range files {
				if f.IsDir() || !re.MatchString(f.Name()) {
					continue
				}
				if seen[f.Name()] {
					continue
				}
				seen[f.Name()] = true
				refs = append(refs, ref{name: f.Name(), path: filepath.Join(dir, f.Name())})
			}
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].name > refs[j].name })
	if limit > 0 && len(refs) > limit {
		refs = refs[:limit]
	}
	out := make([]releasedigest.ReleaseDigest, 0, len(refs))
	for _, r := range refs {
		data, err := os.ReadFile(r.path)
		if err != nil {
			continue
		}
		var d releasedigest.ReleaseDigest
		if err := json.Unmarshal(data, &d); err != nil {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

// MergeReleaseDigests unions today's digest into history, merging same
// period+date entries by repo/tag key so a second run the same day keeps both.
func MergeReleaseDigests(history []releasedigest.ReleaseDigest, today releasedigest.ReleaseDigest) ([]releasedigest.ReleaseDigest, releasedigest.ReleaseDigest) {
	out := make([]releasedigest.ReleaseDigest, 0, len(history)+1)
	merged := today
	for _, h := range history {
		if h.Date == today.Date && releasePeriod(h.Period) == releasePeriod(today.Period) {
			merged = mergeRelease(merged, h)
			continue
		}
		out = append(out, h)
	}
	merged.Count = releasedigest.CountOf(merged)
	merged.Degraded = releasedigest.DegradedOf(merged)
	merged.ContentHash = releasedigest.ContentHash(merged)
	out = append(out, merged)
	return out, merged
}

func mergeRelease(a, b releasedigest.ReleaseDigest) releasedigest.ReleaseDigest {
	idx := map[string]int{}
	for i, r := range a.Repos {
		idx[r.FullName] = i
	}
	for _, rb := range b.Repos {
		i, ok := idx[rb.FullName]
		if !ok {
			a.Repos = append(a.Repos, rb)
			idx[rb.FullName] = len(a.Repos) - 1
			continue
		}
		have := map[string]bool{}
		for _, it := range a.Repos[i].Releases {
			have[releasedigest.ItemKey(it)] = true
		}
		for _, it := range rb.Releases {
			if !have[releasedigest.ItemKey(it)] {
				a.Repos[i].Releases = append(a.Repos[i].Releases, it)
			}
		}
		a.Repos[i].NewCount = len(a.Repos[i].Releases)
	}
	return a
}
