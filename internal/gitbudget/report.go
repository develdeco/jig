package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The report's cuts: callers across all packages, and callers and tests
// within one package. The full counts go to callers.tsv.
const (
	topOverall = 20
	topPackage = 10
)

// callersFile is the full attribution, written into the count directory.
const callersFile = "callers.tsv"

// counted is one name (a caller or a test) and how many git processes it
// started.
type counted struct {
	name  string
	count int
}

// ranked returns m's entries by count descending, then name.
func ranked(m map[string]int) []counted {
	out := make([]counted, 0, len(m))
	for name, n := range m {
		out = append(out, counted{name, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return out[i].name < out[j].name
	})
	return out
}

// top returns the first n of list.
func top(list []counted, n int) []counted {
	if len(list) > n {
		return list[:n]
	}
	return list
}

// share is count's percentage of total, one decimal.
func share(count, total int) string {
	if total == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", float64(count)*100/float64(total))
}

// attributions holds each checked package's attribution; a package that
// started no git process has none.
type attributions map[string]attribution

// overallCallers sums the callers across the packages.
func (as attributions) overallCallers() (map[string]int, int) {
	sum, total := map[string]int{}, 0
	for _, a := range as {
		total += a.total
		for name, n := range a.callers {
			sum[name] += n
		}
	}
	return sum, total
}

// formatAttribution renders the report that follows the per-package table:
// the top callers across the packages, then each package's top callers and
// tests, as Markdown when markdown is set. It is empty when no process was
// attributed to anything, as when nothing was traced.
func formatAttribution(as attributions, markdown bool) string {
	sum, total := as.overallCallers()
	if total == 0 {
		return ""
	}
	var out strings.Builder
	all := ranked(sum)
	if markdown {
		fmt.Fprintf(&out, "\n### Top %d callers across the packages\n\nThe Go function that asked gitx for the git process; %s counts the processes no caller was recorded for.\n\n| caller | count | share |\n|---|---:|---:|\n", topOverall, unattributed)
		for _, c := range top(all, topOverall) {
			fmt.Fprintf(&out, "| %s | %d | %s |\n", c.name, c.count, share(c.count, total))
		}
	} else {
		fmt.Fprintf(&out, "\ntop %d callers across the packages (%d git processes):\n", topOverall, total)
		for _, c := range top(all, topOverall) {
			fmt.Fprintf(&out, "%8d %7s  %s\n", c.count, share(c.count, total), c.name)
		}
	}
	for _, pkg := range sortedKeys(as) {
		a := as[pkg]
		if a.total == 0 {
			continue
		}
		for _, kind := range []struct {
			title string
			m     map[string]int
		}{{"callers", a.callers}, {"tests", a.tests}} {
			list := top(ranked(kind.m), topPackage)
			if markdown {
				fmt.Fprintf(&out, "\n#### %s: top %s (%d git processes)\n\n| %s | count | share |\n|---|---:|---:|\n", pkg, kind.title, a.total, strings.TrimSuffix(kind.title, "s"))
				for _, c := range list {
					fmt.Fprintf(&out, "| %s | %d | %s |\n", c.name, c.count, share(c.count, a.total))
				}
			} else {
				fmt.Fprintf(&out, "\n%s: top %s (%d git processes):\n", pkg, kind.title, a.total)
				for _, c := range list {
					fmt.Fprintf(&out, "%8d %7s  %s\n", c.count, share(c.count, a.total), c.name)
				}
			}
		}
	}
	return out.String()
}

// encodeCallers renders the full attribution as TSV: package, kind (caller or
// test), name, count; sorted by package, kind, then count descending and
// name. A package with no trace files is omitted. A name holding a tab or
// newline cannot break a row: both are written as a space.
func encodeCallers(as attributions) []byte {
	clean := strings.NewReplacer("\t", " ", "\n", " ", "\r", " ")
	var out strings.Builder
	for _, pkg := range sortedKeys(as) {
		a := as[pkg]
		if a.total == 0 {
			continue
		}
		for _, kind := range []struct {
			name string
			m    map[string]int
		}{{"caller", a.callers}, {"test", a.tests}} {
			for _, c := range ranked(kind.m) {
				fmt.Fprintf(&out, "%s\t%s\t%s\t%d\n", pkg, kind.name, clean.Replace(c.name), c.count)
			}
		}
	}
	return []byte(out.String())
}

// writeCallers writes callers.tsv into countDir.
func writeCallers(countDir string, as attributions) error {
	return os.WriteFile(filepath.Join(countDir, callersFile), encodeCallers(as), 0o644)
}
