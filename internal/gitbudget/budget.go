package main

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// budgetHeader opens every budget file write produces.
const budgetHeader = `# The git processes each package's tests may start, counted on CI's Linux
# leg by internal/gitbudget (go test -exec), one per process git traces.
# A count more than margin percent (and at least minSlack processes) away
# from its budget, either way, fails the leg: a change that adds git work to
# the suite raises its package's number here, in its own diff, and one that
# cuts git work lowers it. The failing step prints the numbers to put here.
# A package that starts no git process has no entry.
`

// minSlack is the least a count may move from its budget whatever the
// margin, so a package with a handful of processes is not failed by one.
const minSlack = 2

// budget is a parsed budget file.
type budget struct {
	Margin   int            `yaml:"margin"`
	Packages map[string]int `yaml:"packages"`
}

// readBudget reads and checks a budget file.
func readBudget(path string) (*budget, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var b budget
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if b.Margin < 0 || b.Margin > 100 {
		return nil, fmt.Errorf("%s: margin %d is not a percentage", path, b.Margin)
	}
	for pkg, n := range b.Packages {
		if n <= 0 {
			return nil, fmt.Errorf("%s: %s has budget %d; a package that starts no git process has no entry", path, pkg, n)
		}
	}
	if b.Packages == nil {
		b.Packages = map[string]int{}
	}
	return &b, nil
}

// slack is how far a count may move from limit and still pass.
func (b *budget) slack(limit int) int {
	return max(minSlack, (limit*b.Margin+99)/100)
}

// row is one package's line of a check.
type row struct {
	pkg     string
	limit   int
	count   int
	verdict string // "ok", "over", "under"
}

// check compares counts, which holds every package that ran, with the
// budget, and reports whether each is within its margin.
func (b *budget) check(counts map[string]int) ([]row, bool) {
	var rows []row
	ok := true
	for _, pkg := range sortedKeys(counts) {
		r := row{pkg: pkg, limit: b.Packages[pkg], count: counts[pkg], verdict: "ok"}
		switch s := b.slack(r.limit); {
		case r.count > r.limit+s:
			r.verdict, ok = "over", false
		case r.count < r.limit-s:
			r.verdict, ok = "under", false
		}
		if r.limit == 0 && r.count == 0 {
			continue
		}
		rows = append(rows, r)
	}
	return rows, ok
}

// set records counts as the packages' budgets; a package that started no
// git process loses its entry.
func (b *budget) set(counts map[string]int) {
	for pkg, n := range counts {
		if n == 0 {
			delete(b.Packages, pkg)
			continue
		}
		b.Packages[pkg] = n
	}
}

// encode renders the budget file, packages sorted.
func (b *budget) encode() []byte {
	var out strings.Builder
	out.WriteString(budgetHeader)
	fmt.Fprintf(&out, "margin: %d\npackages:\n", b.Margin)
	for _, pkg := range sortedKeys(b.Packages) {
		fmt.Fprintf(&out, "  %s: %d\n", pkg, b.Packages[pkg])
	}
	return []byte(out.String())
}

// formatRows renders a check as a table.
func formatRows(rows []row, margin int) string {
	var out strings.Builder
	fmt.Fprintf(&out, "git processes per package against .github/git-budget.yaml (margin %d%%, at least %d):\n", margin, minSlack)
	fmt.Fprintf(&out, "%-32s %8s %8s %8s  %s\n", "package", "budget", "count", "change", "verdict")
	for _, r := range rows {
		fmt.Fprintf(&out, "%-32s %8d %8d %+8d  %s\n", r.pkg, r.limit, r.count, r.count-r.limit, r.verdict)
	}
	return out.String()
}

// sortedKeys returns m's keys sorted.
func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
