package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// bodyOf is the rows of a frame between the header's rule and the first of the
// tail's heading or the keys' rule.
func bodyOf(view string) []string {
	lines := strings.Split(view, "\n")
	var body []string
	for i := 2; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "tail ─") || strings.HasPrefix(trimmed, "──────") {
			break
		}
		body = append(body, lines[i])
	}
	return body
}

// wantBoxesClosed checks that every box of the body has its top edge and its
// bottom edge, whatever the window cut.
func wantBoxesClosed(t *testing.T, view string) {
	t.Helper()
	open := false
	for _, l := range bodyOf(view) {
		trimmed := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(trimmed, "╭"):
			if open {
				t.Errorf("a box is opened inside another:\n%s", view)
			}
			open = true
		case strings.HasPrefix(trimmed, "╰"):
			if !open {
				t.Errorf("a bottom edge has no top edge:\n%s", view)
			}
			open = false
		case strings.HasPrefix(trimmed, "│") && !open:
			t.Errorf("a box row has no top edge above it:\n%s", view)
		}
	}
	if open {
		t.Errorf("a box is left open at the bottom of the body:\n%s", view)
	}
}

// TestWindowNeverCutsABoxWithoutItsEdges: a window that cuts a box still draws
// the box, its top edge with its title and its bottom edge, at the terminals a
// tail is opened on.
func TestWindowNeverCutsABoxWithoutItsEdges(t *testing.T) {
	t.Parallel()
	ids := [3]string{"round-totals", "fix-4-cart-test", "cart-total"}
	for _, size := range [][2]int{{60, 20}, {80, 24}} {
		w, h := size[0], size[1]
		t.Run(fmt.Sprintf("ticket %dx%d", w, h), func(t *testing.T) {
			t.Parallel()
			g := longStepsRig(t, w, h, ids, false)
			wantBoxesClosed(t, g.view())
			g.press("t")
			wantBoxesClosed(t, g.view())
			g.press("down")
			wantBoxesClosed(t, g.view())
		})
		t.Run(fmt.Sprintf("plan %dx%d", w, h), func(t *testing.T) {
			t.Parallel()
			titles := map[string]string{}
			var tasks []string
			for i := 1; i <= 3; i++ {
				id := fmt.Sprintf("SHOP-%020d", i)
				tasks = append(tasks, id)
				titles[id] = longTitle(i)
			}
			g := planRig(t, planSpec{tasks: tasks, titles: titles, landed: map[string]bool{tasks[0]: true}})
			g.send(tea.WindowSizeMsg{Width: w, Height: h})
			wantBoxesClosed(t, g.view())
			g.press("t")
			wantBoxesClosed(t, g.view())
			g.press("down", "down")
			wantBoxesClosed(t, g.view())
		})
	}
}

// manyTickets is the main screen with a roster longer than any terminal.
func manyTickets(t *testing.T, w, h int) *rig {
	t.Helper()
	g := newRig(t, "")
	var rows []rosterRow
	for i := 1; i <= 60; i++ {
		rows = append(rows, rosterRow{ID: fmt.Sprintf("JIG-%d", i), Title: fmt.Sprintf("Ticket number %d", i),
			Digest: task("build", false, "done|approved", "current|1 of 2 slices green", "pending|after the build", "pending|after a clean review", "pending|after the publish")})
	}
	g.m.w.setRoster(rows)
	g.send(tea.WindowSizeMsg{Width: w, Height: h})
	return g
}

// TestWindowMarksWhatLiesAboveAndBelow: a window that does not show the page's
// first line starts with "↑ N more" and one that does not show its last line
// ends with "↓ N more", each in a row of the window's own height.
func TestWindowMarksWhatLiesAboveAndBelow(t *testing.T) {
	t.Parallel()
	g := manyTickets(t, 80, 24)
	body := bodyOf(g.view())
	if got := strings.TrimSpace(body[len(body)-1]); !strings.HasPrefix(got, "↓ ") || !strings.HasSuffix(got, " more") {
		t.Errorf("the top of a long page does not end with a mark of what is below:\n%s", g.view())
	}
	for _, l := range body {
		if strings.Contains(l, "↑") {
			t.Errorf("the top of the page marks something above it:\n%s", g.view())
		}
	}
	g.press("down", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down")
	view := g.view()
	body = bodyOf(view)
	if got := strings.TrimSpace(body[0]); !strings.HasPrefix(got, "↑ ") || !strings.HasSuffix(got, " more") {
		t.Errorf("the middle of a long page does not start with a mark of what is above:\n%s", view)
	}
	if got := strings.TrimSpace(body[len(body)-1]); !strings.HasPrefix(got, "↓ ") {
		t.Errorf("the middle of a long page does not end with a mark of what is below:\n%s", view)
	}
	if !strings.Contains(view, "▸") || len(strings.Split(view, "\n")) != 24 {
		t.Errorf("the cursor is out of the window, or the frame is not 24 rows:\n%s", view)
	}
	g.press("end")
	body = bodyOf(g.view())
	for _, l := range body {
		if strings.Contains(l, "↓") {
			t.Errorf("the foot of the page marks something below it:\n%s", g.view())
		}
	}
}

// TestTheReviewScreenOpenedBelowTheFoldShowsItsMark: a screen read from a row
// below the fold says there is more above.
func TestTheReviewScreenOpenedBelowTheFoldShowsItsMark(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.send(tea.WindowSizeMsg{Width: 80, Height: 12})
	g.feed(jig2Mid()...).press("enter")
	if g.m.Screen() != "review" {
		t.Fatalf("on %s, want review", g.m.Screen())
	}
	g.press("end")
	view := g.view()
	body := bodyOf(view)
	if got := strings.TrimSpace(body[0]); !strings.HasPrefix(got, "↑ ") {
		t.Errorf("the review read from its last row does not start with a mark of what is above:\n%s", view)
	}
}

// TestATailThatCannotFitSaysSo: a screen with fewer than 8 body rows puts one
// line where the tail would be, naming what it needs and what the screen has.
func TestATailThatCannotFitSaysSo(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.send(tea.WindowSizeMsg{Width: 80, Height: 12})
	g.feed(jig2Mid()...)
	g.press("t")
	view := g.view()
	if !strings.Contains(view, "the tail needs 8 body rows, this screen has ") {
		t.Errorf("a tail that cannot fit does not say so:\n%s", view)
	}
	if n := len(strings.Split(view, "\n")); n != 12 {
		t.Errorf("the frame is %d rows, want 12:\n%s", n, view)
	}
	g.send(tea.WindowSizeMsg{Width: 80, Height: 40})
	if view := g.view(); strings.Contains(view, "the tail needs") || !strings.Contains(view, "tail ─") {
		t.Errorf("a tall screen draws its tail:\n%s", view)
	}
}
