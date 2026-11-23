package tui

import (
	"strings"
	"time"

	"github.com/develdeco/jig/internal/finalrun"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// The final run is the ticket's last check, of the union of its slices' commands
// at the head the review passed. The screens do not read it from the journal
// themselves: internal/finalrun is the one reading, which publish's report and
// body call too, over the same inputs (the latest round's reviewed head of each
// repo), so a screen and a publish say the same final run of the same ticket.

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
// there is no reviewed head. A head the status prints bare in a project of
// several repos (the ticket builds in one of them) is the head of the repo its
// final-oracle line names.
func (t *ticket) finalRuns() []finalrun.Report {
	heads := t.reviewedHeads()
	if heads == nil {
		return nil
	}
	lines := make([]journal.Line, len(t.lines))
	for i, l := range t.lines {
		lines[i] = l.Line
	}
	qualified := len(heads) > 1
	for repo := range heads {
		qualified = qualified || repo != ""
	}
	if bare, ok := heads[""]; ok && len(heads) == 1 {
		for _, l := range lines {
			if l.Event == journal.EventFinalOracle && l.Commit == bare && l.Repo != "" {
				heads, qualified = map[string]string{l.Repo: bare}, true
			}
		}
	}
	return finalrun.Of(lines, qualified, heads, "", "")
}

// finalSub is the build's sub-step for the final run: done with its outcome
// when every repo's reviewed head has one (failed when any failed), and when one
// has none, pending, saying so.
func finalSub(runs []finalrun.Report) phase.Sub {
	s := phase.Sub{Key: subFinal, Label: "final run", State: phase.SubDone, Detail: "passed"}
	for _, r := range runs {
		switch {
		case r.Outcome == finalrun.None:
			return phase.Sub{Key: subFinal, Label: "final run", State: phase.SubPending, Detail: "none at the reviewed head " + short(r.Head)}
		case r.Outcome == finalrun.Fail:
			s.Detail = "failed"
		}
	}
	return s
}

// finalSeconds is how long the final runs recorded for the reviewed heads ran,
// added up.
func finalSeconds(runs []finalrun.Report) time.Duration {
	var d time.Duration
	for _, r := range runs {
		d += time.Duration(r.Seconds) * time.Second // a head with no run has none
	}
	return d
}
