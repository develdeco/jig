package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// wheel and click are the mouse events a terminal reports.
func wheel(g *rig, up bool) {
	g.t.Helper()
	b := tea.MouseButtonWheelDown
	if up {
		b = tea.MouseButtonWheelUp
	}
	g.send(tea.MouseMsg{X: 10, Y: 6, Button: b, Action: tea.MouseActionPress})
}

func click(g *rig, y int) {
	g.t.Helper()
	g.send(tea.MouseMsg{X: 6, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
}

// termRow is the terminal row of the first line of the frame holding text, -1
// when none does.
func termRow(frame, text string) int {
	for i, l := range strings.Split(frame, "\n") {
		if strings.Contains(l, text) {
			return i
		}
	}
	return -1
}

// TestWheelMovesTheCursor: a tick of the wheel moves the cursor three rows, up
// and down, inside the rows there are; a page with no rows scrolls three lines;
// and the keys list scrolls while it is up.
func TestWheelMovesTheCursor(t *testing.T) {
	t.Parallel()
	g := planRig(t, planSpec{multi: true})
	rows := len(g.m.page().refs())
	if rows < 5 {
		t.Fatalf("the plan has %d rows", rows)
	}
	g.press("home")
	wheel(g, false)
	if g.m.top().sel != 3 {
		t.Errorf("a tick down put the cursor on row %d, want 3", g.m.top().sel)
	}
	wheel(g, false)
	if got := g.m.top().sel; got != min(6, rows-1) {
		t.Errorf("two ticks down put the cursor on row %d, want %d", got, min(6, rows-1))
	}
	g.press("end")
	wheel(g, false)
	if g.m.top().sel != rows-1 {
		t.Errorf("a tick past the end left the cursor on row %d, want %d", g.m.top().sel, rows-1)
	}
	wheel(g, true)
	if g.m.top().sel != rows-4 {
		t.Errorf("a tick up put the cursor on row %d, want %d", g.m.top().sel, rows-4)
	}
	if g.m.Screen() != "chart" {
		t.Errorf("the wheel opened %s", g.m.Screen())
	}

	// A page with no rows to stand on scrolls three lines.
	s := sized(can1(t, landStop()), 80, 10)
	s.press("5")
	if s.m.Screen() != "land" || len(s.m.page().refs()) != 0 || len(s.m.page().lines) <= s.m.bodyRows()+3 {
		t.Fatalf("the land screen does not scroll here: %s, %d rows, %d lines for %d", s.m.Screen(), len(s.m.page().refs()), len(s.m.page().lines), s.m.bodyRows())
	}
	wheel(s, false)
	if s.m.top().scroll != 3 {
		t.Errorf("a tick scrolled the page %d lines, want 3", s.m.top().scroll)
	}
	wheel(s, true)
	wheel(s, true)
	if s.m.top().scroll != 0 {
		t.Errorf("ticks up scrolled the page to %d, want 0", s.m.top().scroll)
	}

	// The keys list scrolls under it and the screen under it does not move.
	k := sized(can1(t, reviewReady()), 80, 12)
	k.press("3", "?")
	sel := k.m.top().sel
	wheel(k, false)
	if k.m.help.scroll != 3 || k.m.top().sel != sel {
		t.Errorf("a tick scrolled the keys %d and moved the cursor to %d", k.m.help.scroll, k.m.top().sel)
	}
	wheel(k, true)
	wheel(k, true)
	if k.m.help.scroll != 0 {
		t.Errorf("ticks up scrolled the keys to %d", k.m.help.scroll)
	}

	// Nothing is read of a wheel while a question is being typed.
	q := can1(t, reviewReady())
	q.m.ask = &confirmation{prompt: "sure?"}
	before := q.m.top().sel
	wheel(q, false)
	if q.m.top().sel != before {
		t.Errorf("the wheel moved the cursor under a question")
	}
}

// TestClickSelectsARow: a click on a row of the body puts the cursor on it and
// opens nothing; a click on the header, the hint line, the panel or a line that
// is no row does nothing; a page windowed past its first rows is read through
// the window drawn.
func TestClickSelectsARow(t *testing.T) {
	t.Parallel()

	// A list.
	g := mainRig(t)
	rows := ticketRows(g)
	frame := g.view()
	y := termRow(frame, rows[2])
	if y < 0 {
		t.Fatalf("main does not draw %s:\n%s", rows[2], frame)
	}
	click(g, y)
	if ref := g.m.selected(); g.m.Screen() != "main" || ref == nil || ref.id != rows[2] {
		t.Errorf("a click on %s's row left %s with the cursor on %+v", rows[2], g.m.Screen(), ref)
	}
	// Not rows: the header, the rule under it, the hint line.
	for _, y := range []int{0, 1, len(strings.Split(frame, "\n")) - 1} {
		before := g.m.top().sel
		click(g, y)
		if g.m.top().sel != before || g.m.Screen() != "main" {
			t.Errorf("a click on terminal row %d moved the cursor to %d", y, g.m.top().sel)
		}
	}
	// A right click and a release do nothing.
	g.send(tea.MouseMsg{X: 6, Y: termRow(g.view(), rows[0]), Button: tea.MouseButtonRight, Action: tea.MouseActionPress})
	g.send(tea.MouseMsg{X: 6, Y: termRow(g.view(), rows[0]), Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	if ref := g.m.selected(); ref == nil || ref.id != rows[2] {
		t.Errorf("a right click or a release moved the cursor to %+v", ref)
	}

	// A plan.
	p := planRig(t, planSpec{})
	tasks := ticketRows(p)
	py := termRow(p.view(), tasks[2])
	click(p, py)
	if ref := p.m.selected(); p.m.Screen() != "chart" || ref == nil || ref.id != tasks[2] {
		t.Errorf("a click on %s in the plan left %s on %+v", tasks[2], p.m.Screen(), ref)
	}
	// A line that is no row: the plan's first body line is a panel edge.
	before := p.m.top().sel
	click(p, 2)
	if p.m.top().sel != before {
		t.Errorf("a click on a panel's edge moved the cursor to %d", p.m.top().sel)
	}

	// A page windowed past its first rows: the row drawn under the pointer is
	// the one selected, and the page does not move under it.
	w := sized(planRig(t, planSpec{multi: true}), 80, 14)
	w.press("end", "up", "up")
	frame = w.view()
	const target = "SHOP-4"
	if termRow(frame, target) < 0 || strings.Contains(frame, "Need") {
		t.Fatalf("the window is not past the first rows of the page:\n%s", frame)
	}
	click(w, termRow(frame, target))
	if ref := w.m.selected(); ref == nil || ref.id != target {
		t.Errorf("a click on %s in a windowed page selected %+v", target, ref)
	}

	// The mark of what lies above, and the edge of the box the window starts in,
	// are no rows: a click on the row above the first line drawn selects nothing.
	before = w.m.top().sel
	if w.m.hit == nil || w.m.hit.top == 0 {
		t.Fatal("the frame recorded no window")
	}
	click(w, w.m.hit.top-1)
	if w.m.top().sel != before {
		t.Errorf("a click on the mark above the window moved the cursor to %d", w.m.top().sel)
	}

	// The panel and the keys list are not rows either: with ? up a click does nothing.
	k := can1(t, reviewReady())
	k.press("3")
	k.press("?")
	sel := k.m.top().sel
	click(k, 4)
	if k.m.top().sel != sel || k.m.help == nil {
		t.Errorf("a click with the keys up moved the cursor or closed them")
	}
}

// TestMouseIsOnlyOnAScreen: the program asks the terminal for the mouse only
// when the screens own it; a pipe is given no mouse and writes none of its
// reporting sequences.
func TestMouseIsOnlyOnAScreen(t *testing.T) {
	t.Parallel()
	if s := setupFor(true); !s.mouse || !s.altScreen || len(s.options()) != 2 {
		t.Errorf("a terminal's setup: %+v with %d options", s, len(s.options()))
	}
	if s := setupFor(false); s.mouse || s.altScreen || len(s.options()) != 0 {
		t.Errorf("a pipe's setup: %+v with %d options", s, len(s.options()))
	}

	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := Run(ctx, Config{Width: 80, Height: 24}, strings.NewReader("q"), &out, false); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(out.String(), "\x1b[?100") || out.Len() == 0 {
		t.Errorf("a pipe was sent mouse reporting or nothing: %q", out.String())
	}
}
