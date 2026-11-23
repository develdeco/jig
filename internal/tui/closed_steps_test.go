package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestClosedTicketTellsUnfinishedStepsFromThePhasesDoneMarks: phase.Derive marks
// every phase of a closed ticket done, so the digest names the phases it had not
// done when it closed, and every screen (the project's list, the chart's row and
// the ticket's screen, from the first frame, with no status read) draws them
// closed; a step it finished keeps its words, and a publish it never made keeps
// the reason it closed in its words under the closed mark.
func TestClosedTicketTellsUnfinishedStepsFromThePhasesDoneMarks(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		unfinished []string
		closed     []int
		dots       string
	}{
		"nothing finished": {[]string{"brief", "build", "review", "publish"}, []int{stepBrief, stepBuild, stepReview, stepPublish}, "⊖ ⊖ ⊖ ⊖ ⊖"},
		"brief and build":  {[]string{"review", "publish"}, []int{stepReview, stepPublish}, "✓ ✓ ⊖ ⊖ ⊖"},
		"closed published": {[]string{"review"}, []int{stepReview}, "✓ ✓ ⊖ ✓ ⊖"},
	} {
		done := task("publish", false, "done|approved by Ada", "done|1 slice green", "done|ready for a fresh session to review the change", "done|closed · not planned", "done|none · closed without a pull request")
		done.Closed, done.Unfinished = "not planned", tc.unfinished
		g := newRig(t, "")
		g.m.w.setRoster([]rosterRow{{ID: "JIG-9", Title: "Ticket", State: "ready", Digest: done}})
		tk := g.m.w.tickets["JIG-9"]
		steps := g.m.steps(tk)
		isClosed := map[int]bool{}
		for _, i := range tc.closed {
			isClosed[i] = true
		}
		for i := stepBrief; i <= stepPublish; i++ {
			if got := steps[i].State == StateClosed; got != isClosed[i] {
				t.Errorf("%s: step %d is %s %q, closed should be %v", name, i+1, steps[i].State, steps[i].Text, isClosed[i])
			}
			switch {
			case i == stepPublish && isClosed[i]:
				if steps[i].Text != "closed · not planned" {
					t.Errorf("%s: the publish never made lost the reason: %q", name, steps[i].Text)
				}
			case isClosed[i]:
				if steps[i].Text != "closed" {
					t.Errorf("%s: step %d says %q, want closed", name, i+1, steps[i].Text)
				}
			case steps[i].Text != done.Phases[i].Text:
				t.Errorf("%s: the finished step %d lost its words: %q", name, i+1, steps[i].Text)
			}
		}
		if steps[stepLand].State != StateClosed || steps[stepLand].Text != "none · closed without a pull request" {
			t.Errorf("%s: the land step is %+v", name, steps[stepLand])
		}
		// The same dots on the project's list, with no status read.
		if row := lineWith(pagesText(g.m.mainPage()), "JIG-9"); !strings.Contains(row, tc.dots) {
			t.Errorf("%s: main's row does not draw %s:\n%s", name, tc.dots, row)
		}
	}
}

// TestLandedTicketOfOneRepoIsNotOwedAMerge: a landed line of a project of one repo
// names no repo while the digest's pull request does, and the two are paired: the
// land screen lists one landed pull request, no merge to make, and no repo name.
func TestLandedTicketOfOneRepoIsNotOwedAMerge(t *testing.T) {
	t.Parallel()
	d := task("land", false, "done|a", "done|b", "done|c", "done|pull request opened", "done|landed")
	d.PRs = []digestPR{{Repo: "app", Number: 22, URL: "https://x/22", State: "merged", Target: "main"}}
	g := landTicket(t, d, jl(at(15, 30), "JIG-2", "", journal.EventLanded, func(l *journal.Line) {
		l.Base, l.Provenance = "main", journal.ProvenanceAgent
	}))
	g.press("5")
	view := g.view()
	for _, want := range []string{"1 of 1 landed", "✓ PR #22 into main · landed at 09:15:30"} {
		if !strings.Contains(view, want) {
			t.Errorf("the land screen lacks %q:\n%s", want, view)
		}
	}
	for _, bad := range []string{"merge PR #22", "app  ", "its target"} {
		if strings.Contains(view, bad) {
			t.Errorf("the land screen says %q of a landed ticket:\n%s", bad, view)
		}
	}
}
