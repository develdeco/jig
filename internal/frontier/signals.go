package frontier

import (
	"strings"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/staircase"
)

// measureSignals measures a lease's staircase signals over the range
// startSHA..HEAD: whether any file changed (from `git diff --name-only`)
// matches an invariant the repo declares. The caller adds the slice's own
// failed attempts, which the lease cannot tell.
func measureSignals(leaseDir, startSHA string, m manifest.Manifest) staircase.Signals {
	var sig staircase.Signals
	if out, err := gitx.Run(leaseDir, "diff", "--name-only", startSHA+"..HEAD"); err == nil {
		if trimmed := strings.TrimSpace(out); trimmed != "" {
			sig.Invariant = anyPathMatchesInvariant(trimmed, m)
		}
	}
	return sig
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
