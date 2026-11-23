package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
)

// closedStatus is `jig status SHOP-1` of a ticket closed before its work was done
// (approved says whether its brief was approved, green whether its slice is).
func unfinishedClosedStatus(approved, green bool) string {
	approval, slice := "none", "queued"
	if approved {
		approval = "current"
	}
	if green {
		slice = "green"
	}
	return strings.Join([]string{
		"ticket: SHOP-1", "state: closed",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{{"round", slice, "1", "-", "-", "Round"}}),
		"questions: none",
		axi.KV("brief", [][2]string{{"approval", approval}, {"approved_by", "Dana <d@example.invalid>"}, {"provenance", "person"}}),
		axi.Table("brief_sections", []string{"heading", "slices"}, [][]string{{"Goal", "round"}}),
		axi.KV("closed", [][2]string{{"reason", "not-planned"}}),
	}, "\n") + "\n"
}

// TestClosedTicketTellsUnfinishedStepsFromThePhasesDoneMarks: phase.Derive marks
// every phase of a closed ticket done, so the steps it never finished are told
// from its status: a brief nobody approved, a slice that is not green and a
// review with no round read closed, and a step it finished keeps its words.
func TestClosedTicketTellsUnfinishedStepsFromThePhasesDoneMarks(t *testing.T) {
	t.Parallel()
	done := task("publish", false, "done|drafted · waiting for your approval", "done|0 of 1 green", "done|ready for a fresh session to review the change", "done|closed · not planned", "done|none · closed without a pull request")
	done.Closed = "not planned"
	for name, tc := range map[string]struct {
		approved, green bool
		closed          []int
	}{
		"nothing finished": {false, false, []int{stepBrief, stepBuild, stepReview}},
		"brief and build":  {true, true, []int{stepReview}},
	} {
		g := newRig(t, "SHOP-1")
		g.r.answer("status SHOP-1 --store /s", unfinishedClosedStatus(tc.approved, tc.green), 0)
		g.feed(jl(at(0, 0), "SHOP-1", "", journal.EventNote))
		g.pin("SHOP-1", done)
		g.syncDigests()
		tk := g.m.w.tickets["SHOP-1"]
		if tk.status == nil || tk.status.Closed == "" {
			t.Fatalf("%s: the status was not read as closed: %+v", name, tk.status)
		}
		steps := g.m.steps(tk)
		isClosed := map[int]bool{}
		for _, i := range tc.closed {
			isClosed[i] = true
		}
		for i := stepBrief; i <= stepReview; i++ {
			if got := steps[i].State == StateClosed && steps[i].Text == "closed"; got != isClosed[i] {
				t.Errorf("%s: step %d is %s %q, closed should be %v", name, i+1, steps[i].State, steps[i].Text, isClosed[i])
			}
			if !isClosed[i] && steps[i].Text != done.Phases[i].Text {
				t.Errorf("%s: the finished step %d lost its words: %q", name, i+1, steps[i].Text)
			}
		}
		if steps[stepLand].State != StateClosed || steps[stepLand].Text != "none · closed without a pull request" {
			t.Errorf("%s: the land step is %+v", name, steps[stepLand])
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
