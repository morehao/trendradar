package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/morehao/trendradar/internal/digest"
)

// Index renders an index of the most recent digests (newest first).
func Index(digests []digest.Digest, limit int) []byte {
	ds := make([]digest.Digest, len(digests))
	copy(ds, digests)
	sort.Slice(ds, func(i, j int) bool { return ds[i].Date > ds[j].Date })
	if limit > 0 && len(ds) > limit {
		ds = ds[:limit]
	}

	var b strings.Builder
	b.WriteString("# 趋势索引\n\n")
	b.WriteString("| 日期 | 数量 | 降级 | 摘要 |\n|---|---|---|---|\n")
	for _, d := range ds {
		fmt.Fprintf(&b, "| %s | %d | %t | [%s](digests/%s/%s.md) |\n",
			d.Date, d.Count, d.Degraded, d.Date, d.Date, d.Date)
	}
	return []byte(b.String())
}
