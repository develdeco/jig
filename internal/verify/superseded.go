package verify

import (
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
)

// Superseded reports whether verifying sha for slice is history: another
// commit of the slice that descends from sha already has an outcome of its own
// (an oracle line, passed or failed, or a run that could not start).
// Verifications run in detached workers that finish in any order, so a slow
// run of an older commit can be journaled after a newer commit's; what decides
// is the commits' order in git, which isAncestor reports, never the order of
// the journal's lines.
func Superseded(lines []journal.Line, slice, sha string, isAncestor func(ancestor, descendant string) bool) bool {
	if sha == "" || isAncestor == nil {
		return false
	}
	for _, l := range lines {
		if l.Slice != slice || l.Commit == "" || l.Commit == sha {
			continue
		}
		if l.Event != journal.EventOracle && l.Event != journal.EventVerifyError {
			continue
		}
		if isAncestor(sha, l.Commit) {
			return true
		}
	}
	return false
}

// GitAncestry is Superseded's isAncestor for the repository at dir. A commit
// git cannot place there (an unknown sha, no repository at all) is no one's
// ancestor, so an outcome nobody can order is never taken for history.
func GitAncestry(dir string) func(ancestor, descendant string) bool {
	return func(ancestor, descendant string) bool {
		if dir == "" {
			return false
		}
		ok, err := gitx.IsAncestor(dir, ancestor, descendant)
		return err == nil && ok
	}
}
