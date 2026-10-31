package render

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/morehao/trendradar/internal/digest"
)

// Per-period artifact filename patterns. A file only counts when it matches its
// period's pattern, so stray files (e.g. a README) in a bucket are ignored.
var (
	dailyFileRe   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\.json$`)
	monthlyFileRe = regexp.MustCompile(`^\d{4}-\d{2}\.json$`)
	weeklyFileRe  = regexp.MustCompile(`^\d{4}-W\d{2}\.json$`)
)

func periodFileRe(period string) *regexp.Regexp {
	switch period {
	case PeriodWeekly:
		return weeklyFileRe
	case PeriodMonthly:
		return monthlyFileRe
	default:
		return dailyFileRe
	}
}

// ListDigests walks root/<period>/<bucket>/ and returns the newest digests of
// each period, up to limits[period], for building the index. Missing period
// directories, empty buckets and non-matching files are ignored (never an
// error). Within a period the artifact names sort lexicographically in date
// order (YYYY-MM-DD, YYYY-Wnn, YYYY-MM), so a filename sort is a date sort.
func ListDigests(root string, limits map[string]int) ([]digest.Digest, error) {
	var out []digest.Digest
	for _, period := range []string{PeriodDaily, PeriodWeekly, PeriodMonthly} {
		ds, err := listPeriod(root, period, limits[period])
		if err != nil {
			return nil, err
		}
		out = append(out, ds...)
	}
	return out, nil
}

func listPeriod(root, period string, limit int) ([]digest.Digest, error) {
	periodDir := filepath.Join(root, period)
	buckets, err := os.ReadDir(periodDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	re := periodFileRe(period)
	type ref struct{ name, path string }
	var refs []ref
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
			refs = append(refs, ref{name: f.Name(), path: filepath.Join(dir, f.Name())})
		}
	}

	sort.Slice(refs, func(i, j int) bool { return refs[i].name > refs[j].name })
	if limit > 0 && len(refs) > limit {
		refs = refs[:limit]
	}

	out := make([]digest.Digest, 0, len(refs))
	for _, r := range refs {
		data, err := os.ReadFile(r.path)
		if err != nil {
			continue
		}
		var d digest.Digest
		if err := json.Unmarshal(data, &d); err != nil {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}
