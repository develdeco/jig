package tui

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

var newMark = regexp.MustCompile(`↑ \d+ new`)

// markedRow is the row of a frame the cursor stands on.
func markedRow(frame string) string {
	for _, l := range strings.Split(frame, "\n") {
		if strings.HasPrefix(l, "▸") || strings.Contains(l, "│▸") {
			return l
		}
	}
	return ""
}

// TestACursorStaysOnItsRowWhenLinesComeInAbove: with the cursor on the older of
// two rounds, a newer one that comes in is a row above it, and the cursor is
// still on the older one, and so is the detail under the list.
func TestACursorStaysOnItsRowWhenLinesComeInAbove(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-6")
	g.r.answer("status JIG-6 --store /s", fixSliceStatus(true), 0)
	g.feed(fixSliceLines()...).press("down", "enter")
	if g.m.Screen() != "review" {
		t.Fatalf("on %s, want review", g.m.Screen())
	}
	g.press("down") // round 2 is on top, round 1 under it
	if row := markedRow(g.view()); !strings.Contains(row, "round 1") {
		t.Fatalf("the cursor is on %q, want round 1:\n%s", row, g.view())
	}
	g.feed(
		jl(at(33, 0), "JIG-6", "", journal.EventGateOpen),
		jl(at(33, 30), "JIG-6", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = 3, "s-reviewer" }),
	)
	v := g.view()
	if row := markedRow(v); !strings.Contains(row, "round 1") {
		t.Errorf("the cursor left its row: it is on %q:\n%s", row, v)
	}
	if !strings.Contains(v, "╭─ Detail · round 1") {
		t.Errorf("the detail left the row the cursor is on:\n%s", v)
	}
	inOrder(t, "rounds", between(v, "Rounds", "╰"), "round 3", "round 2", "round 1")

	// A row that went away leaves the cursor where it was, as before.
	g2 := newRig(t, "JIG-6")
	g2.r.answer("status JIG-6 --store /s", fixSliceStatus(true), 0)
	g2.feed(fixSliceLines()...).press("down", "enter", "down")
	if g2.m.top().sel != 1 {
		t.Fatalf("the cursor is at %d, want 1", g2.m.top().sel)
	}
}

// TestAPageHeldBelowItsTopCountsTheLinesThatCameInAbove: a slice's timeline
// read down the screen keeps the lines it shows when two come in above, says "↑
// 2 new" in its first row, and says nothing once it is read back to its top.
func TestAPageHeldBelowItsTopCountsTheLinesThatCameInAbove(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", slicesStatus([]string{"a", "building", "1", "-", "-", "Greet", "app", "unit", "-"}), 0)
	for i := 0; i < 40; i++ {
		g.feed(jl(at(i, 0), "JIG-1", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")))
	}
	g.press("enter", "enter")
	if g.m.Screen() != "slice" {
		t.Fatalf("on %s, want slice", g.m.Screen())
	}
	for i := 0; i < 12; i++ {
		g.press("down")
	}
	body := func() []string { return strings.Split(g.view(), "\n")[2:] }
	before := body()
	if newMark.MatchString(g.view()) {
		t.Fatalf("a mark before any line came in:\n%s", g.view())
	}
	g.feed(
		jl(at(41, 0), "JIG-1", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(42, 0), "JIG-1", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")),
	)
	after := body()
	// The count is a row of the box the window cuts, under its top edge and its
	// names.
	newAt := -1
	for i, l := range after {
		if strings.TrimSpace(strings.Trim(strings.TrimSpace(l), "│")) == "↑ 2 new" {
			newAt = i
		}
	}
	if newAt < 0 || newAt > 3 {
		t.Fatalf("no row reads \"↑ 2 new\" at the top of the box:\n%s", g.view())
	}
	// The lines it showed stay where they were: the count took the place of the
	// mark that said there was more above.
	for i := newAt + 1; i < len(before)-4; i++ {
		if before[i] != after[i] {
			t.Fatalf("row %d was %q and is now %q:\n%s", i, before[i], after[i], g.view())
		}
	}
	// Read to its end with the mark up, the page still shows its last row: the
	// count of the older lines the timeline left out.
	for i := 0; i < 200; i++ {
		g.press("down")
	}
	if v := g.view(); !newMark.MatchString(v) || !strings.Contains(v, "↓ 2 older") {
		t.Errorf("the end of the page is not in view under the mark:\n%s", v)
	}
	for i := 0; i < 300; i++ {
		g.press("up")
	}
	if v := g.view(); newMark.MatchString(v) || g.m.top().fresh != 0 {
		t.Errorf("the mark stays at the top:\n%s", v)
	}
}

// TestTheCountOfNewLinesNeverHidesTheCursor: a page of rows read below its top
// shows the cursor's row under the count wherever the cursor stands: on the last
// row, in the middle, at the window's last line with the window held above it,
// and at the window's first line when the window also ends the page.
func TestTheCountOfNewLinesNeverHidesTheCursor(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.m.top().fresh = 2
	for name, c := range map[string]struct{ rows, plain, sel, lead int }{
		"the last row":              {30, 0, 29, 0},
		"a row in the middle":       {30, 0, 20, 0},
		"the window's last line":    {30, 0, 15, 4},
		"the first line, page ends": {21, 9, 20, 0},
	} {
		var pg page
		for i := 0; i < c.rows; i++ {
			pg.addRow(rowRef{kind: rowSlice, id: strconv.Itoa(i)}, "row "+strconv.Itoa(i))
		}
		for i := 0; i < c.plain; i++ {
			pg.add("plain " + strconv.Itoa(i))
		}
		g.m.top().lead = c.lead
		start := g.m.windowStart(pg, c.sel, 10, true)
		above := ""
		if g.m.counting(start) {
			above = g.m.newRow(g.m.top().fresh)
		}
		out, _, _ := g.m.windowRows(pg, c.sel, start, 10, 80, above)
		if !newMark.MatchString(out[0]) {
			t.Errorf("%s: the first row reads %q, want the count", name, out[0])
		}
		marked := ""
		for _, l := range out {
			if strings.HasPrefix(l, "▸") {
				marked = l
			}
		}
		if want := "row " + strconv.Itoa(c.sel); !strings.Contains(marked, want) {
			t.Errorf("%s: the marked row is %q, want %q in view:\n%s", name, marked, want, strings.Join(out, "\n"))
		}
	}
}
