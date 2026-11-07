package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// baselineHeader opens every baseline file write produces.
const baselineHeader = `# The git processes each package's tests started on main's last Linux run,
# counted by internal/gitbudget (go test -exec), one per process git traces.
# A package that starts no git process has no entry.
`

// config is the tunable threshold: a count may exceed its baseline by the
// larger of margin percent of the baseline (rounded up) and slack processes.
type config struct {
	Margin int `yaml:"margin"`
	Slack  int `yaml:"slack"`
}

// baseline is a parsed baseline file: main's measured count per package.
type baseline struct {
	Packages map[string]int `yaml:"packages"`
}

// readConfig reads and checks the config file.
func readConfig(path string) (*config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.Margin < 0 || c.Margin > 100 {
		return nil, fmt.Errorf("%s: margin %d is not a percentage", path, c.Margin)
	}
	if c.Slack < 0 {
		return nil, fmt.Errorf("%s: slack %d is negative", path, c.Slack)
	}
	return &c, nil
}

// readBaseline reads a baseline file. A file that does not exist is not an
// error: it returns nil, nil, and the check records that there is none.
func readBaseline(path string) (*baseline, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var b baseline
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for pkg, n := range b.Packages {
		if n <= 0 {
			return nil, fmt.Errorf("%s: %s has count %d; a package that starts no git process has no entry", path, pkg, n)
		}
	}
	if b.Packages == nil {
		b.Packages = map[string]int{}
	}
	return &b, nil
}

// allowed is how far a count may rise above base and still pass.
func (c *config) allowed(base int) int {
	return max((base*c.Margin+99)/100, c.Slack)
}

// Verdicts of a row.
const (
	verdictOK       = "ok"
	verdictOver     = "over"
	verdictNew      = "new"
	verdictNotRun   = "not run"
	verdictAccepted = "accepted"
)

// row is one package's line of a check.
type row struct {
	pkg     string
	base    int
	count   int
	ran     bool
	verdict string
}

// check compares counts, which holds every package that ran, with the
// baseline. A count above its baseline plus the allowance is over, and the
// check fails unless accepted turns the overs into accepted passes. A
// package with no baseline entry has baseline 0; a baseline package that did
// not run is shown and never fails.
func (c *config) check(base map[string]int, counts map[string]int, accepted bool) ([]row, bool) {
	var rows []row
	ok := true
	for _, pkg := range sortedKeys(counts) {
		r := row{pkg: pkg, base: base[pkg], count: counts[pkg], ran: true, verdict: verdictOK}
		if r.base == 0 && r.count == 0 {
			continue
		}
		_, inBase := base[pkg]
		switch {
		case r.count > r.base+c.allowed(r.base):
			if accepted {
				r.verdict = verdictAccepted
			} else {
				r.verdict, ok = verdictOver, false
			}
		case !inBase:
			r.verdict = verdictNew
		}
		rows = append(rows, r)
	}
	for _, pkg := range sortedKeys(base) {
		if _, ran := counts[pkg]; !ran {
			rows = append(rows, row{pkg: pkg, base: base[pkg], verdict: verdictNotRun})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].pkg < rows[j].pkg })
	return rows, ok
}

// encodeBaseline renders the baseline file for counts, packages sorted; a
// package that started no git process has no entry.
func encodeBaseline(counts map[string]int) []byte {
	var out strings.Builder
	out.WriteString(baselineHeader)
	out.WriteString("packages:\n")
	for _, pkg := range sortedKeys(counts) {
		if counts[pkg] > 0 {
			fmt.Fprintf(&out, "  %s: %d\n", pkg, counts[pkg])
		}
	}
	return []byte(out.String())
}

// cells are a row's columns; the count, change and percent are "-" for a
// package that did not run, and the percent where the baseline is 0.
func (r row) cells() (base, count, change, percent string) {
	base, count, change, percent = fmt.Sprint(r.base), "-", "-", "-"
	if !r.ran {
		return
	}
	count = fmt.Sprint(r.count)
	change = fmt.Sprintf("%+d", r.count-r.base)
	if r.base > 0 {
		percent = fmt.Sprintf("%+.1f%%", float64(r.count-r.base)*100/float64(r.base))
	}
	return
}

// formatRows renders a check as a plain table.
func formatRows(rows []row, c *config) string {
	var out strings.Builder
	fmt.Fprintf(&out, "git processes per package against main's baseline (margin %d%%, slack %d):\n", c.Margin, c.Slack)
	fmt.Fprintf(&out, "%-32s %8s %8s %8s %8s  %s\n", "package", "baseline", "count", "change", "percent", "verdict")
	for _, r := range rows {
		b, n, ch, p := r.cells()
		fmt.Fprintf(&out, "%-32s %8s %8s %8s %8s  %s\n", r.pkg, b, n, ch, p, r.verdict)
	}
	return out.String()
}

// markdownRows renders a check as a Markdown table for the job summary.
func markdownRows(rows []row, c *config) string {
	var out strings.Builder
	fmt.Fprintf(&out, "### Git processes per package\n\nAgainst main's baseline (margin %d%%, slack %d).\n\n", c.Margin, c.Slack)
	out.WriteString("| package | baseline | count | change | percent | verdict |\n|---|---:|---:|---:|---:|---|\n")
	for _, r := range rows {
		b, n, ch, p := r.cells()
		fmt.Fprintf(&out, "| %s | %s | %s | %s | %s | %s |\n", r.pkg, b, n, ch, p, r.verdict)
	}
	return out.String()
}

// noBaselineNote is the line a check prints when main has no baseline.
const noBaselineNote = "no main baseline: counts shown, nothing compared"

// formatCounts renders counts for a check with no baseline, as a Markdown
// table when markdown is set.
func formatCounts(counts map[string]int, markdown bool) string {
	var out strings.Builder
	if markdown {
		fmt.Fprintf(&out, "### Git processes per package\n\n%s.\n\n| package | count |\n|---|---:|\n", noBaselineNote)
	} else {
		fmt.Fprintf(&out, "%s\n%-32s %8s\n", noBaselineNote, "package", "count")
	}
	for _, pkg := range sortedKeys(counts) {
		if counts[pkg] == 0 {
			continue
		}
		if markdown {
			fmt.Fprintf(&out, "| %s | %d |\n", pkg, counts[pkg])
		} else {
			fmt.Fprintf(&out, "%-32s %8d\n", pkg, counts[pkg])
		}
	}
	return out.String()
}

// sortedKeys returns m's keys sorted.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
