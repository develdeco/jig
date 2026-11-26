package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
)

// TestAClosedTicketBeforeTheRosterNeverReadsLanded: with only its status in, a
// closed ticket's header says closed and its steps say reading; none is ticked,
// and nothing says landed until the digest tells the end state.
func TestAClosedTicketBeforeTheRosterNeverReadsLanded(t *testing.T) {
	t.Parallel()
	g := newRig(t, "SHOP-1")
	g.r.answer("status SHOP-1 --store /s", closedStatusOf(), 0)
	g.feed(jl(at(0, 0), "SHOP-1", "", journal.EventNote))
	tk := g.m.w.tickets["SHOP-1"]
	tk.digest = nil
	if tk.status == nil || tk.status.Closed == "" {
		t.Fatalf("the status was not read as closed: %+v", tk.status)
	}
	if state, label := g.m.stepLabel(tk); state != StateClosed || label != "closed" {
		t.Errorf("the header says %s %q, want closed", state, label)
	}
	for i, s := range g.m.steps(tk) {
		if s.Done || s.State == StateOK || s.Text != readingText {
			t.Errorf("step %d is %+v before the roster, want reading and not done", i+1, s)
		}
	}
	if page := pagesText(g.m.ticketPage("SHOP-1")); strings.Contains(page, "landed") {
		t.Errorf("the screen says landed before the roster:\n%s", page)
	}
}

// closedStatusOf is a closed ticket's status with its brief approved and its
// slice green.
func closedStatusOf() string {
	return strings.Join([]string{
		"ticket: SHOP-1", "state: closed",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{{"round", "green", "1", "-", "-", "Round"}}),
		"questions: none",
		axi.KV("brief", [][2]string{{"approval", "current"}, {"approved_by", "Dana <d@example.invalid>"}, {"provenance", "person"}}),
		axi.KV("closed", [][2]string{{"reason", "not-planned"}}),
	}, "\n") + "\n"
}

// TestLandScreenOfABehindTicketListsNoMerge: a ticket published and then behind,
// or built again, owes a publish before any merge: its land screen lists no merge
// to make, and says so.
func TestLandScreenOfABehindTicketListsNoMerge(t *testing.T) {
	t.Parallel()
	d := task("publish", true, "done|a", "done|b", "done|c", "current|"+behindText, "pending|after the publish")
	d.Behind = true
	d.PRs = []digestPR{{Repo: "app", Number: 8, URL: "https://x/8", State: "open", Target: "main"}}
	g := landTicket(t, d)
	g.press("5")
	view := g.view()
	if strings.Contains(view, "merge PR #8") || !strings.Contains(view, "no merge to make until it publishes again") {
		t.Errorf("a behind ticket's land screen:\n%s", view)
	}
	if !strings.Contains(view, "after the publish") {
		t.Errorf("the screen does not say what the ticket waits for:\n%s", view)
	}
}
