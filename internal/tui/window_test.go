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

// TestWrapLongKeepsEveryRowWithinTheScreen: a line too long for the screen wraps
// to rows that are all within its room, loose or in a box, at word breaks or in
// the middle of an unbroken token; its text is whole across the rows, and only
// its first row carries the cursor.
func TestWrapLongKeepsEveryRowWithinTheScreen(t *testing.T) {
	t.Parallel()
	for _, w := range []int{40, 60} {
		for _, boxed := range []bool{false, true} {
			g := newRig(t, "")
			g.send(tea.WindowSizeMsg{Width: w, Height: 30})
			for _, text := range []string{"  see " + strings.Repeat("x", 50), "  " + strings.TrimSpace(strings.Repeat("word ", 18))} {
				var pg page
				ref := &rowRef{kind: rowSlice, id: "s"}
				if boxed {
					pg.lines = append(pg.lines, pline{text: text, ref: ref, box: boxMid})
				} else {
					pg.lines = append(pg.lines, pline{text: text, ref: ref})
				}
				room := w - 2
				if boxed {
					room = g.m.innerWidth()
				}
				got := g.m.wrapLong(pg)
				var flat strings.Builder
				for i, l := range got.lines {
					if width(l.text) > room {
						t.Errorf("w=%d boxed=%v: row %d is %d cells in a room of %d: %q", w, boxed, i, width(l.text), room, l.text)
					}
					if (i == 0) != (l.ref != nil) || (i > 0) != l.cont {
						t.Errorf("w=%d boxed=%v: row %d has ref %v and cont %v, want the ref on the first row alone", w, boxed, i, l.ref != nil, l.cont)
					}
					flat.WriteString(strings.TrimSpace(l.text))
				}
				if want := strings.ReplaceAll(strings.TrimSpace(text), " ", ""); strings.ReplaceAll(flat.String(), " ", "") != want {
					t.Errorf("w=%d boxed=%v: the text is cut: %q, want %q", w, boxed, flat.String(), want)
				}
				if again := g.m.wrapLong(got); len(again.lines) != len(got.lines) {
					t.Errorf("w=%d boxed=%v: wrapping again changes %d rows into %d", w, boxed, len(got.lines), len(again.lines))
				}
			}
		}
	}
}

// TestEveryFrameIsTheTerminalsHeight: with the tail open on a terminal of 10 to
// 16 rows, no screen draws more rows than the terminal has, with the cursor
// on every row of it, and no mark counts nothing.
func TestEveryFrameIsTheTerminalsHeight(t *testing.T) {
	t.Parallel()
	ids := [3]string{"round-totals", "fix-4-cart-test", "cart-total"}
	titles := map[string]string{}
	var tasks []string
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("SHOP-%020d", i)
		tasks = append(tasks, id)
		titles[id] = longTitle(i)
	}
	rigs := map[string]func(h int) *rig{
		"ticket": func(h int) *rig { return longStepsRig(t, 80, h, ids, true) },
		"plan": func(h int) *rig {
			g := planRig(t, planSpec{tasks: tasks, titles: titles, landed: map[string]bool{tasks[0]: true}})
			g.send(tea.WindowSizeMsg{Width: 80, Height: h})
			return g
		},
		"chart": func(h int) *rig {
			g := chartScreenRig(t)
			g.send(tea.WindowSizeMsg{Width: 80, Height: h})
			return g
		},
	}
	for name, mk := range rigs {
		for h := 10; h <= 16; h++ {
			g := mk(h)
			g.press("t")
			for step := 0; step < 14; step++ {
				view := g.view()
				if n := len(strings.Split(view, "\n")); n > h {
					t.Fatalf("%s at 80x%d, step %d: the frame is %d rows, over the terminal's:\n%s", name, h, step, n, view)
				}
				if strings.Contains(view, "↑ 0 ") || strings.Contains(view, "↓ 0 ") {
					t.Fatalf("%s at 80x%d, step %d: a mark counts nothing:\n%s", name, h, step, view)
				}
				g.press("down")
			}
		}
	}
}

// TestAWindowTooShortForItsMarksDrawsPlainLines: a window with no row to spare
// for the marks and edges of the box it is in draws its lines plain, all the
// rows it has, so the frame stays the terminal's height.
func TestAWindowTooShortForItsMarksDrawsPlainLines(t *testing.T) {
	t.Parallel()
	var pg page
	pg.boxTop("Box", "")
	for i := 0; i < 10; i++ {
		pg.boxAdd(fmt.Sprintf("line %d", i))
	}
	pg.boxEnd()
	from, to, plain := windowSpan(pg, 0, 4, 3, false)
	if !plain || from != 4 || to != 7 {
		t.Errorf("a window of 3 rows inside a box is lines %d to %d (plain %v), want 4 to 7 plain", from, to, plain)
	}
	if from, to, plain = windowSpan(pg, 0, 4, 8, false); plain || to-from < 3 {
		t.Errorf("a window of 8 rows is lines %d to %d (plain %v), want marks and at least 3 lines", from, to, plain)
	}
	// The marks count the page lines above and below the window, and the box
	// keeps its edges: the top edge and 4 above, 4 lines, 4 below and the edge.
	g := newRig(t, "")
	g.send(tea.WindowSizeMsg{Width: 40, Height: 30})
	rows, from, to := g.m.windowRows(pg, 0, 4, 8, 40, "")
	if len(rows) != 8 || from != 4 || to != 8 || !strings.Contains(rows[1], "↑ 4 more") || !strings.Contains(rows[6], "↓ 4 more") ||
		!strings.Contains(rows[0], "╭") || !strings.Contains(rows[7], "╰") {
		t.Errorf("the window of 8 rows is %d rows, lines %d to %d:\n%s", len(rows), from, to, strings.Join(rows, "\n"))
	}
}

// TestEveryWindowDrawsTheCursorsRow: on the ticket, the plan and the chart, at 60
// and 80 columns by 8 to 24 rows, with the tail open and closed, the cursor
// moved down through twenty rows: the window never starts on a continuation row,
// it holds the cursor's row, the frame draws the cursor's mark, and a plain
// window frames the box lines it draws.
func TestEveryWindowDrawsTheCursorsRow(t *testing.T) {
	t.Parallel()
	ids := [3]string{"round-totals", "fix-4-cart-test", "cart-total"}
	titles := map[string]string{}
	var tasks []string
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("SHOP-%020d", i)
		tasks = append(tasks, id)
		titles[id] = longTitle(i)
	}
	rigs := map[string]func(w, h int) *rig{
		"ticket": func(w, h int) *rig { return longStepsRig(t, w, h, ids, true) },
		"plan": func(w, h int) *rig {
			g := planRig(t, planSpec{tasks: tasks, titles: titles, landed: map[string]bool{tasks[0]: true}})
			g.send(tea.WindowSizeMsg{Width: w, Height: h})
			return g
		},
		"chart": func(w, h int) *rig {
			g := chartScreenRig(t)
			g.send(tea.WindowSizeMsg{Width: w, Height: h})
			return g
		},
	}
	for name, mk := range rigs {
		for _, w := range []int{60, 80} {
			for h := 8; h <= 24; h++ {
				for _, tail := range []bool{false, true} {
					g := mk(w, h)
					if tail {
						g.press("t")
					}
					for step := 0; step < 20; step++ {
						view := g.view()
						l := g.m.layout()
						if len(l.pg.refs()) > 0 {
							start := g.m.windowStart(l.pg, l.sel, l.pageH, true)
							keep := g.m.top().lead == 0
							from, to, plain := windowSpan(l.pg, l.sel, start, l.pageH, keep)
							if from < len(l.pg.lines) && l.pg.lines[from].cont {
								t.Fatalf("%s %dx%d tail %v step %d: the window starts on a continuation row (line %d)\n%s", name, w, h, tail, step, from, view)
							}
							if keep && !strings.Contains(view, "▸") && l.pageH >= 2 {
								t.Fatalf("%s %dx%d tail %v step %d: no row of the frame has the cursor (window %d to %d, plain %v)\n%s", name, w, h, tail, step, from, to, plain, view)
							}
							if plain {
								rows, _, _ := g.m.windowRows(l.pg, l.sel, start, l.pageH, w, "")
								for i, row := range rows {
									if l.pg.lines[from+i].box != boxNone && !strings.Contains(row, "│") && !strings.Contains(row, "╭") && !strings.Contains(row, "╰") {
										t.Fatalf("%s %dx%d tail %v step %d: a plain window draws the box line %q with no frame", name, w, h, tail, step, row)
									}
								}
							}
						}
						g.press("down")
					}
				}
			}
		}
	}
}
