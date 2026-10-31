package render

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/morehao/trendradar/internal/digest"
)

// Index renders the digest index grouped into daily / weekly / monthly sections.
// Within each section entries are newest first and capped at limits[period].
// Empty sections are omitted so a daily-only checkout stays clean.
//
// root is the digest root inside the repository so per-row links stay correct
// when output.dir is not the default "digests".
func Index(digests []digest.Digest, limits map[string]int, root string) []byte {
	root = OutputRoot(root)

	byPeriod := map[string][]digest.Digest{}
	for _, d := range digests {
		p := PeriodOf(d)
		byPeriod[p] = append(byPeriod[p], d)
	}

	var b strings.Builder
	b.WriteString("# 趋势索引\n")
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
			link := path.Join(root, period, BucketOf(period, d.Date), name+".md")
			fmt.Fprintf(&b, "| %s | %d | %t | [%s](%s) |\n",
				d.Date, d.Count, d.Degraded, name, link)
		}
	}
	return []byte(b.String())
}

func periodTitle(period string) string {
	switch period {
	case PeriodWeekly:
		return "周榜"
	case PeriodMonthly:
		return "月榜"
	default:
		return "日榜"
	}
}
