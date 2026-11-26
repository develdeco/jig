package tui

import (
	"strings"
	"testing"
)

// TestAStepKeyFromAScreenWithNoTicketScreenPutsOneUnder: a ticket's screens
// reached without its own screen under them still end with esc on the ticket.
func TestAStepKeyFromAScreenWithNoTicketScreenPutsOneUnder(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...)
	g.m.nav = []navEntry{g.m.nav[0], {screen: screenBuild, ticket: "JIG-1"}}
	g.view()
	g.press("3")
	if got := strings.Join(stack(g), ">"); got != "main>ticket>review" {
		t.Errorf("the stack is %s, want main>ticket>review", got)
	}
}

// TestTheReviewScreenKeepsWhereItWasLeft: the review screen opens on the first
// ask when it is new, and where the person left the cursor when it is not.
func TestTheReviewScreenKeepsWhereItWasLeft(t *testing.T) {
	t.Parallel()
	g := can1(t, reviewOpen())
	g.press("3")
	first := g.m.top().sel
	if g.m.Screen() != "review" || g.m.selected() == nil || g.m.selected().kind != rowAsk {
		t.Fatalf("the review opened on %+v, want an ask", g.m.selected())
	}
	g.press("down")
	moved := g.m.top().sel
	if moved == first {
		t.Fatalf("down did not move the cursor off row %d", first)
	}
	g.press("2", "3")
	if g.m.top().sel != moved {
		t.Errorf("the review opened with its cursor on row %d, want %d where it was left", g.m.top().sel, moved)
	}
}
