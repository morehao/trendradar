package render

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/morehao/trendradar/internal/digest"
)

// Index renders an index of the most recent digests (newest first). root is the
// digest root inside the repository so per-row links stay correct when
// output.dir is not the default "digests".
func Index(digests []digest.Digest, limit int, root string) []byte {
	root = OutputRoot(root)
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
		link := path.Join(root, d.Date, d.Date+".md")
		fmt.Fprintf(&b, "| %s | %d | %t | [%s](%s) |\n",
			d.Date, d.Count, d.Degraded, d.Date, link)
	}
	return []byte(b.String())
}
