package tui

import (
	"strings"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// The final run is the ticket's last check, of the union of its slices' commands
// at the head the review passed. Its one source is the line `jig verify --final`
// recorded for that head, per repo (journal.FinalRunAt, which publish reads too):
// its outcome and the seconds it ran, never a gap between lines and never a run
// at another commit.

// finalRun is the final run recorded for one repo's reviewed head.
type finalRun struct {
	repo, head string
	line       journal.Line
	ok         bool
}

// reviewedHeads are the heads of the latest gate round by repo ("" for a
// project of one repo), nil when no round was recorded.
func (t *ticket) reviewedHeads() map[string]string {
	if t.status == nil || len(t.status.Rounds) == 0 {
		return nil
	}
	head := t.status.Rounds[len(t.status.Rounds)-1].Head
	if head == "" || head == "-" {
		return nil
	}
	out := map[string]string{}
	if !strings.Contains(head, ":") {
		out[""] = head
		return out
	}
	for _, part := range strings.Split(head, "+") {
		if repo, sha, ok := strings.Cut(part, ":"); ok {
			out[repo] = sha
		}
	}
	return out
}

// finalRuns are the final runs at the reviewed head of each repo, nil when
// there is no reviewed head.
func (t *ticket) finalRuns() []finalRun {
	heads := t.reviewedHeads()
	if heads == nil {
		return nil
	}
	lines := make([]journal.Line, len(t.lines))
	for i, l := range t.lines {
		lines[i] = l.Line
	}
	var out []finalRun
	for repo, head := range heads {
		line, ok := journal.FinalRunAt(lines, repo, head)
		out = append(out, finalRun{repo, head, line, ok})
	}
	sortRuns(out)
	return out
}

// sortRuns puts the runs in repo order.
func sortRuns(runs []finalRun) {
	for i := 1; i < len(runs); i++ {
		for j := i; j > 0 && runs[j].repo < runs[j-1].repo; j-- {
			runs[j], runs[j-1] = runs[j-1], runs[j]
		}
	}
}

// finalSub is the build's sub-step for the final run: done with its outcome
// when every repo's reviewed head has one (failed when any failed), and when one
// has none, pending, saying so.
func finalSub(runs []finalRun) phase.Sub {
	s := phase.Sub{Key: subFinal, Label: "final run", State: phase.SubDone, Detail: "passed"}
	for _, r := range runs {
		switch {
		case !r.ok:
			return phase.Sub{Key: subFinal, Label: "final run", State: phase.SubPending, Detail: "none at the reviewed head " + short(r.head)}
		case r.line.Outcome == "fail":
			s.Detail = "failed"
		}
	}
	return s
}

// finalSeconds is how long the final runs recorded for the reviewed heads ran,
// added up.
func finalSeconds(runs []finalRun) time.Duration {
	var d time.Duration
	for _, r := range runs {
		d += time.Duration(r.line.Seconds) * time.Second // a head with no run has the zero line
	}
	return d
}
