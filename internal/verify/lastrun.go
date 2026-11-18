package verify

import "github.com/develdeco/jig/internal/journal"

// The words a last run says whether it covers its slice's last commit in.
const (
	// CoversYes: the run covers the commit the slice's latest trailer commit
	// on the branch is.
	CoversYes = "yes"
	// CoversNotChecked: the branch was not read, so no commit is known to be
	// the slice's last.
	CoversNotChecked = "not checked"
	// CoversNoPrefix starts every other answer: "no: <why>".
	CoversNoPrefix = "no: "
)

// LastRun is the last run of `jig verify` a slice has: its latest oracle line,
// covering or not (an uncovered run is the last thing witnessed, so it is
// shown with why it covers nothing), and whether that run covers the slice's
// last commit.
type LastRun struct {
	Slice string
	// Line is the oracle line.
	Line journal.Line
	// Result is the line's outcome and cause in a word: pass, fail, stopped or
	// timed out.
	Result string
	// Covers is "yes", "not checked", or "no: " and the reason: the run's own
	// uncovered reason, or the commit the slice's last one is.
	Covers string
}

// resultWords is an oracle line's outcome and cause in a word.
func resultWords(l journal.Line) string {
	switch {
	case l.Outcome == "fail" && l.Cause == journal.CauseStopped:
		return "stopped"
	case l.Outcome == "fail" && l.Cause == journal.CauseTimeout:
		return "timed out"
	case l.Outcome == "":
		return "ran"
	}
	return l.Outcome
}

// LastRuns is each slice's last run, in the order the slices' first oracle
// lines appear. The slice's last commit is read from the branch's commits the
// Truth already holds (the latest one carrying the slice's trailer past the
// ticket's start sha), so it costs no git process of its own.
func (t Truth) LastRuns() []LastRun {
	latest := map[string]journal.Line{}
	var order []string
	for _, l := range t.Lines {
		if l.Event != journal.EventOracle || l.Slice == "" {
			continue
		}
		if _, ok := latest[l.Slice]; !ok {
			order = append(order, l.Slice)
		}
		latest[l.Slice] = l
	}
	lastCommit := map[string]string{}
	for _, c := range t.Commits { // oldest first
		lastCommit[c.Slice] = c.SHA
	}
	out := make([]LastRun, 0, len(order))
	for _, slice := range order {
		l := latest[slice]
		run := LastRun{Slice: slice, Line: l, Result: resultWords(l)}
		switch last, known := lastCommit[slice]; {
		case l.Uncovered != "":
			run.Covers = CoversNoPrefix + l.Uncovered
		case !t.BranchKnown:
			run.Covers = CoversNotChecked
		case !known:
			run.Covers = CoversNoPrefix + "slice has no commit on the branch"
		case last != l.Commit:
			run.Covers = CoversNoPrefix + "slice's last commit is " + short12(last)
		default:
			run.Covers = CoversYes
		}
		out = append(out, run)
	}
	return out
}

func short12(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
