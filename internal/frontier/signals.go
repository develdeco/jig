package frontier

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/staircase"
)

// shortstatCountRE matches the insertion/deletion counts in a
// `git diff --shortstat` line, e.g. "2 files changed, 10 insertions(+), 3
// deletions(-)".
var shortstatCountRE = regexp.MustCompile(`(\d+) insertion|(\d+) deletion`)

// measureSignals measures a lease's staircase signals over the range
// startSHA..HEAD: total changed lines (from `git diff --shortstat`), files
// changed (from `git diff --name-only`), and whether any file changed matches
// a declared invariant.
func measureSignals(leaseDir, startSHA string, m manifest.Manifest) staircase.Signals {
	rangeSpec := startSHA + "..HEAD"
	var sig staircase.Signals

	if out, err := gitx.Run(leaseDir, "diff", "--shortstat", rangeSpec); err == nil {
		sig.DiffLines = sumShortstatCounts(out)
	}
	if out, err := gitx.Run(leaseDir, "diff", "--name-only", rangeSpec); err == nil {
		if trimmed := strings.TrimSpace(out); trimmed != "" {
			sig.DiffFiles = len(strings.Split(trimmed, "\n"))
			sig.Invariant = anyPathMatchesInvariant(trimmed, m)
		}
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

func anyPathMatchesInvariant(nameOnlyOutput string, m manifest.Manifest) bool {
	for _, filePath := range strings.Split(nameOnlyOutput, "\n") {
		if filePath == "" {
			continue
		}
		if m.MatchesInvariant(filePath) {
			return true
		}
	}
	return false
}
