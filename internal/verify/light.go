package verify

import (
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
)

// lightRun is the decision to run the manifest's light oracle in place of the
// slice's: ran is that command, from the full run of reusedFrom.
type lightRun struct {
	ran, reusedFrom string
}

// lightFor decides whether the commit's delta is light. The base is the newest
// passing oracle line of cmd and env on an ancestor of the commit with no
// later failed full run of the same command; only a failed full run with no
// cause stops a light delta (a stop, a kill or a failed light run does not).
// When the files that base changed into the commit all match the manifest's
// light rule, the light oracle stands in for the slice's. Anything jig cannot
// prove (no light rule, no earlier pass, a failed full run since, a file
// outside the rule, a journal or diff it cannot read) returns false and the
// slice's own oracle runs, as before.
func (x *run) lightFor(m manifest.Manifest, cmd string) (lightRun, bool) {
	if !m.Light.Declared() {
		return lightRun{}, false
	}
	lines, err := journal.Read(x.d.Store, x.r.Ticket)
	if err != nil {
		return lightRun{}, false
	}
	isAncestor := GitAncestry(x.d.Clone)
	base, ok := lightBase(lines, cmd, x.sl.Env, x.sha, isAncestor)
	if !ok {
		return lightRun{}, false
	}
	files, err := gitx.DiffNameOnly(x.dir, base.Commit, x.sha, "ACDMRTUXB")
	if err != nil || !m.Light.Matches(files) {
		return lightRun{}, false
	}
	origin := base.Commit
	if base.ReusedFrom != "" {
		origin = base.ReusedFrom
	}
	return lightRun{ran: m.Light.Oracle, reusedFrom: origin}, true
}

// lightBase finds the oracle line a light delta stands on: walking the journal
// from its end, the first pass of cmd and env on a commit that is an ancestor
// of sha (and not sha itself), unless a failed full run of cmd and env was
// written after it. A failed light run says nothing of the whole suite, so it
// does not stop a later delta from reusing the pass; any other failure of the
// command does, except one that never judged the code (a cause), on whatever commit it ran.
func lightBase(lines []journal.Line, cmd, env, sha string, isAncestor func(ancestor, descendant string) bool) (journal.Line, bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		if l.Event != journal.EventOracle || l.Command != cmd || l.Env != env {
			continue
		}
		switch l.Outcome {
		case "fail":
			// A run that never judged the code (a kill or a stop) says nothing
			// of the suite either.
			if l.Cause == "" && l.Ran == "" {
				return journal.Line{}, false
			}
		case "pass":
			if l.Commit != "" && l.Commit != sha && isAncestor(l.Commit, sha) {
				return l, true
			}
		}
	}
	return journal.Line{}, false
}
