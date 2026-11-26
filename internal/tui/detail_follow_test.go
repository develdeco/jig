package tui

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/journal"
)

// keyNow delivers one key to the model and draws it once, with no other message
// between: the frame a terminal shows after the key.
func keyNow(g *rig, r rune) string {
	g.t.Helper()
	g.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	return g.m.View()
}

// firstFrames opens the review of reviewFixRound's ticket on its ask and returns to
// it from the activity pane, at w x h, and checks the first frame each time is the
// ask's Detail.
func firstFrames(t *testing.T, w, h int) {
	t.Helper()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", reviewFixRoundStatus(), 0)
	g.feed(reviewFixRoundEvents()...)
	g.sized(w, h)
	g.view()
	if first := keyNow(g, '3'); !strings.Contains(first, "╭─ Detail · r2-f2") || strings.Contains(first, "╭─ Detail · round") {
		t.Errorf("%dx%d: the first frame of the review is not the ask's Detail:\n%s", w, h, first)
	}
	g.tabTo("Review activity")
	g.press("esc")
	if first := keyNow(g, '3'); !strings.Contains(first, "╭─ Detail · r2-f2") || strings.Contains(first, "╭─ Detail · round") {
		t.Errorf("%dx%d: the first frame of a return to the review is not the ask's Detail:\n%s", w, h, first)
	}
}

// TestTheDetailIsTheFocusedListsRowInEveryFrame: a screen's panes have their
// cursors from its first frame, so the frame after the key that opens the review
// on its ask, and the one after a return to it, draw the ask's Detail.
func TestTheDetailIsTheFocusedListsRowInEveryFrame(t *testing.T) {
	t.Parallel()
	firstFrames(t, 100, 30)
}

var detailNameRe = regexp.MustCompile(`Detail · (\S+)`)

// detailNamed is the name the Detail of a frame is titled with.
func detailNamed(frame string) string {
	if m := detailNameRe.FindStringSubmatch(frame); m != nil {
		return m[1]
	}
	return "<none>"
}

// reviewOfAsks is the review of a ticket with two asks, at 100 columns and h rows.
func reviewOfAsks(t *testing.T, h int) *rig {
	t.Helper()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", askStatus(askOne, askTwo), 0)
	g.send(tea.WindowSizeMsg{Width: 100, Height: h})
	g.feed(jig2Mid()...)
	g.press("3")
	return g
}

// firstRowOf is the terminal row of the first data row inside the pane titled
// title, past its names.
func firstRowOf(frame, title string) int {
	lines := strings.Split(frame, "\n")
	for i, l := range lines {
		if strings.Contains(l, "╭─ "+title) {
			for j := i + 1; j < len(lines); j++ {
				if !strings.Contains(lines[j], "─") && !strings.Contains(lines[j], "time     step") && strings.TrimSpace(strings.Trim(lines[j], "│ ")) != "" {
					return j
				}
			}
		}
	}
	return -1
}

// TestTheDetailIsTheRowAClickOrAReopenPutTheCursorOn: the Detail is the row of the
// focused list's own cursor, found by identity over all its rows: a click in a
// zoomed pane, a click with a search that narrows the lists, and a reopen from an
// ask with the search kept leave it on the cursor's row, in the first frame too.
func TestTheDetailIsTheRowAClickOrAReopenPutTheCursorOn(t *testing.T) {
	t.Parallel()
	for _, h := range []int{30, 50} {
		// A click in a zoomed pane, and the screen drawn again.
		g := reviewOfAsks(t, h)
		g.tabTo("Review activity")
		g.press("z", "down")
		click(g, firstRowOf(g.view(), "Review activity"))
		g.press("esc")
		if got := detailNamed(g.view()); got != "r1-f1" {
			t.Errorf("100x%d: after a click in the zoomed activity the Detail is %s, want r1-f1", h, got)
		}

		// A click with a search that narrows the lists.
		n := reviewOfAsks(t, h)
		n.press("/")
		typeText(n, "shadows")
		n.press("enter")
		n.tabTo("Rounds")
		click(n, firstRowOf(n.view(), "Review activity"))
		if got := n.view(); !strings.Contains(got, "Detail · round 1") {
			t.Errorf("100x%d: after a click with the lists narrowed the Detail is %s, want round 1:\n%s", h, detailNamed(got), got)
		}

		// A reopen from an ask with the search kept: the first frame is the ask's.
		n.tabTo("Rounds")
		n.press("2", "esc")
		n.tabTo("Needs you")
		n.press("home")
		n.m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		first := n.m.View()
		sel := n.m.selected()
		second := n.m.View()
		if sel == nil || detailNamed(first) != sel.id || detailNamed(first) != detailNamed(second) {
			t.Errorf("100x%d: the first frame of a reopen has the Detail %s, the cursor is %+v, the second frame has %s:\n%s", h, detailNamed(first), sel, detailNamed(second), first)
		}
	}
}

// TestTheFirstFrameDrawsTheCursorRowsDetail: the same at 100x30 and 100x40.
func TestTheFirstFrameDrawsTheCursorRowsDetail(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{100, 30}, {100, 40}} {
		firstFrames(t, size[0], size[1])
	}
}

// TestTheDetailFollowsItsRowNotItsIndex: the Detail is of the row the cursor of
// the focused list stands on, found by the row's identity, so a zoom that keeps
// one pane, a search that narrows a list and a row that comes in above leave it
// on its own row.
func TestTheDetailFollowsItsRowNotItsIndex(t *testing.T) {
	t.Parallel()

	// A zoom: the Asks pane zoomed, tab to the Detail, which is the ask's.
	g := reviewFixRound(t, 100, 30)
	if got := g.view(); !strings.Contains(got, "╭─ Detail · r2-f2") {
		t.Fatalf("the review opens on the ask, whose detail is the Detail:\n%s", got)
	}
	g.press("z", "tab")
	if got := g.m.focusedPane(); got != "Detail · r2-f2" {
		t.Errorf("zoomed on the asks, tab reaches %q, want the ask's Detail", got)
	}
	g.press("z")
	if got := g.view(); !strings.Contains(got, "╭─ Detail · r2-f2") || strings.Contains(got, "╭─ Detail · round") {
		t.Errorf("unzoomed, the Detail is not the ask's:\n%s", got)
	}

	// A search that narrows the slices: the row it lights has its own Detail.
	s := tourRig(t, 100, 30)
	s.press("2").tabTo("Slices")
	s.press("/")
	typeText(s, "fix-1")
	if got := s.m.selected(); got == nil || got.slice != "fix-1-alpha" {
		t.Fatalf("the search puts the cursor on %+v, want fix-1-alpha", got)
	}
	if got := s.view(); !strings.Contains(got, "╭─ Detail · fix-1-alpha") {
		t.Errorf("the Detail is not of the row the search lit:\n%s", got)
	}

	// A round that comes in above, with the focus on another pane: the Detail
	// stays on the ask, whose pane keeps it.
	r := reviewFixRound(t, 100, 40)
	r.tabTo("Review activity")
	r.feed(
		jl(at(14, 0), "JIG-2", "", journal.EventGateOpen),
		jl(at(14, 30), "JIG-2", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = 3, "s-reviewer" }),
	)
	if got := r.view(); !strings.Contains(got, "╭─ Detail · r2-f2") {
		t.Errorf("a round that came in above moved the Detail off the ask:\n%s", got)
	}
}

// TestDownScrollsAScreenWithNoRows: on a screen whose focused pane has no row to
// move to, down reads the page a line at a time, whichever screen it is: the
// slice screen at 60x20, whose one row cannot move, and a page with no row at all.
func TestDownScrollsAScreenWithNoRows(t *testing.T) {
	t.Parallel()
	g := sized(newRig(t, "JIG-1"), 60, 20)
	g.feed(questionTicket()...)
	g.press("2")
	g.tabTo("Slices")
	for i := 0; i < 6 && (g.m.selected() == nil || g.m.selected().slice != "c"); i++ {
		g.press("down")
	}
	g.press("enter")
	if g.m.Screen() != "slice" {
		t.Fatalf("on %s, want the slice screen", g.m.Screen())
	}
	first := g.view()
	g.press("down", "down", "down", "down")
	if g.view() == first {
		t.Errorf("four downs on the slice screen leave the frame as it was:\n%s", first)
	}
}
