package make

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/develdeco/jig/gitx"
	"github.com/develdeco/jig/staircase"
)

// shortstatCountRE matches the insertion/deletion counts in a
// `git diff --shortstat` line, e.g. "2 files changed, 10 insertions(+), 3
// deletions(-)".
var shortstatCountRE = regexp.MustCompile(`(\d+) insertion|(\d+) deletion`)

// measureSignals measures a lease's staircase signals over the range
// startSHA..HEAD: total changed lines (from `git diff --shortstat`), files
// changed (from `git diff --name-only`), and whether any added line matches
// staircase.InvariantRE (from the full diff).
func measureSignals(leaseDir, startSHA string) staircase.Signals {
	rangeSpec := startSHA + "..HEAD"
	var sig staircase.Signals

	if out, err := gitx.Run(leaseDir, "diff", "--shortstat", rangeSpec); err == nil {
		sig.DiffLines = sumShortstatCounts(out)
	}
	if out, err := gitx.Run(leaseDir, "diff", "--name-only", rangeSpec); err == nil {
		if trimmed := strings.TrimSpace(out); trimmed != "" {
			sig.DiffFiles = len(strings.Split(trimmed, "\n"))
		}
	}
	if out, err := gitx.Run(leaseDir, "diff", rangeSpec); err == nil {
		sig.Invariant = diffAddsInvariantLine(out)
	}
	return sig
}

func sumShortstatCounts(shortstat string) int {
	total := 0
	for _, m := range shortstatCountRE.FindAllStringSubmatch(shortstat, -1) {
		for _, g := range m[1:] {
			if g == "" {
				continue
			}
			if n, err := strconv.Atoi(g); err == nil {
				total += n
			}
		}
	}
	return total
}

func diffAddsInvariantLine(diff string) bool {
	for _, line := range strings.Split(diff, "\n") {
		if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+++") {
			continue
		}
		if staircase.InvariantRE.MatchString(line) {
			return true
		}
	}
	return false
}
