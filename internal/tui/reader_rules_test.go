package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestTheHeaderCutsTheTitleBeforeThePlace: on every screen the title gives way
// first and the place among the siblings only when the id alone no longer fits:
// a plan with a 75-character title at 100 columns shows its place.
func TestTheHeaderCutsTheTitleBeforeThePlace(t *testing.T) {
	t.Parallel()
	g := planMain(t, planSpec{second: true})
	onRow(t, g, "repeat")
	g.press("enter")
	g.m.w.charts["repeat"].info.Title = strings.Repeat("a long plan title ", 5)[:75]
	g.send(tea.WindowSizeMsg{Width: 100, Height: 24})
	if h := head(g); !strings.Contains(h, "› repeat (1 of 2)") || !strings.Contains(h, "a long plan") {
		t.Errorf("the plan's header at 100 columns: %q", h)
	}
	// Narrower, the title is cut to nothing before the place goes.
	g.send(tea.WindowSizeMsg{Width: 60, Height: 24})
	if h := head(g); !strings.Contains(h, "› repeat (1 of 2)") {
		t.Errorf("the plan's header at 60 columns lost its place before its title: %q", h)
	}
	// Where the id alone does not fit, the place goes.
	g.send(tea.WindowSizeMsg{Width: 30, Height: 24})
	if h := head(g); strings.Contains(h, "of 2") {
		t.Errorf("the plan's header at 30 columns kept its place: %q", h)
	}
}

// TestASiblingRefusalSaysWhatHolds: a ticket the list never held is in no list of
// the screen, not a ticket that left it.
func TestASiblingRefusalSaysWhatHolds(t *testing.T) {
	t.Parallel()
	g := mainRig(t)
	g.press("enter")
	g.m.top().ticket = "JIG-404"
	g.press("]")
	if want := "JIG-404 is in no list of this screen"; g.m.notice != want {
		t.Errorf("notice %q, want %q", g.m.notice, want)
	}
}

// TestEnterNeverPushesTheScreenItIsOn: enter on an ask of the review screen or a
// question of a slice opens no second copy of the screen, and ? lists enter as
// now only where it opens another screen.
func TestEnterNeverPushesTheScreenItIsOn(t *testing.T) {
	t.Parallel()
	g := sized(can1(t, reviewOpen()), 80, 40)
	g.press("3")
	if ref := g.m.selected(); g.m.Screen() != "review" || ref == nil || ref.kind != rowAsk {
		t.Fatalf("on %s with %+v, want the review on an ask", g.m.Screen(), ref)
	}
	depth := len(g.m.nav)
	g.press("enter")
	if len(g.m.nav) != depth || g.m.Screen() != "review" {
		t.Errorf("enter on an ask left the stack at %v", stack(g))
	}
	if hint := footerKeys(g.view()); strings.Contains(hint, "enter open") {
		t.Errorf("the hint line offers enter where it opens nothing:\n%s", hint)
	}
	g.press("?")
	if v := readWords(g.view()); strings.Contains(v, "enter open now") || !strings.Contains(v, "enter open not now") {
		t.Errorf("? does not say enter opens nothing here:\n%s", g.view())
	}
	g.press("?", "esc")

	// On the ticket's screen it opens the step under the cursor, and says so.
	if hint := footerKeys(g.view()); !strings.Contains(hint, "enter open") {
		t.Errorf("the ticket's hint line lacks enter:\n%s", hint)
	}

	// A question of the slice screen is the slice's own.
	s := newRig(t, "JIG-1")
	s.feed(jig1()...)
	s.press("2", "enter")
	if s.m.Screen() != "slice" {
		t.Fatalf("on %s, want a slice", s.m.Screen())
	}
	depth = len(s.m.nav)
	for range 6 {
		s.press("down")
		s.press("enter")
		if len(s.m.nav) != depth {
			t.Fatalf("enter on the slice screen pushed: %v", stack(s))
		}
	}
}
