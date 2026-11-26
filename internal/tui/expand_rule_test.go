package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
)

// bigSlices is the build screen of a ticket of twelve slices s01 to s12, the one
// numbered at9 titled with a goal of n words.
func bigSlices(t *testing.T, w, h, at9, n int) *rig {
	t.Helper()
	var rows [][]string
	for i := 1; i <= 12; i++ {
		goal := fmt.Sprintf("slice %02d goal", i)
		if i == at9 {
			goal = cutTitle(n)
		}
		rows = append(rows, []string{fmt.Sprintf("s%02d", i), "queued", "0", "-", "-", goal})
	}
	status := strings.Join([]string{"ticket: BIG-1", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, rows), "questions: none",
	}, "\n") + "\n"
	g := newRig(t, "BIG-1")
	g.r.answer("status BIG-1 --store /s", status, 0)
	g.send(tea.WindowSizeMsg{Width: w, Height: h})
	g.feed(jl(at(1, 0), "BIG-1", "s01", journal.EventClaim, with("s-builder", "", "", "", "", "")))
	g.press("enter")
	if g.m.Screen() != "build" {
		t.Fatalf("on %s, want build", g.m.Screen())
	}
	return g
}

// TestARowUnderAMoreMarkExpandsAndIsReadToItsEnd: a list with more rows than its
// window holds counts the ones it leaves out; a cut row below such a count is
// still the cursor's, space is listed and opens it, and it is read to its end.
func TestARowUnderAMoreMarkExpandsAndIsReadToItsEnd(t *testing.T) {
	t.Parallel()
	for _, at := range []int{9, 10} {
		for _, n := range []int{60, 300} {
			g := bigSlices(t, 80, 24, at, n)
			g.tabTo("Slices")
			for i := 1; i < at; i++ {
				g.press("down")
			}
			id := fmt.Sprintf("s%02d", at)
			frame := stripANSI(g.view())
			if atCursor(g) != id || !strings.Contains(strings.Join(strings.Fields(frame), " "), "space expand") {
				t.Fatalf("s%02d n=%d: the cut row under the cursor does not list space (cursor on %s):\n%s", at, n, atCursor(g), frame)
			}
			g.press("space")
			last := fmt.Sprintf("w%03d", n)
			open := stripANSI(g.view())
			for i := 0; i < 400 && !strings.Contains(open, last) && atCursor(g) == id; i++ {
				g.press("down")
				open = stripANSI(g.view())
			}
			if !strings.Contains(open, last) || atCursor(g) != id {
				t.Errorf("s%02d n=%d: not read to %s (cursor on %s):\n%s", at, n, last, atCursor(g), open)
			}
		}
	}
}

// outsideFrame is the frame with the lines of the pane titled title taken out.
func outsideFrame(frame, title string) []string {
	in := false
	var out []string
	for _, l := range strings.Split(frame, "\n") {
		if strings.Contains(l, "╭─ "+title) {
			in = true
		}
		if !in && !strings.Contains(l, "space ") {
			out = append(out, l)
		}
		if in && strings.Contains(l, "╰") {
			in = false
		}
	}
	return out
}

// TestAnExpansionKeepsTheFoldedFrameOutsideItsPane: an expanded row is read to its
// end, and one press past it, inside the pane it is in: every line outside that
// pane equals the folded frame's.
func TestAnExpansionKeepsTheFoldedFrameOutsideItsPane(t *testing.T) {
	t.Parallel()
	check := func(name, pane string, g *rig) {
		t.Helper()
		folded := stripANSI(g.view())
		g.press("space")
		start := atCursor(g)
		for i := 0; i < 80 && atCursor(g) == start; i++ {
			frame := stripANSI(g.view())
			if want, got := outsideFrame(folded, pane), outsideFrame(frame, pane); strings.Join(want, "\n") != strings.Join(got, "\n") {
				t.Errorf("%s: the frame outside %s moved after %d presses:\n--- folded\n%s\n--- now\n%s", name, pane, i, folded, frame)
				return
			}
			g.press("down")
		}
	}
	for _, size := range [][2]int{{60, 20}, {80, 23}, {80, 24}} {
		g := longCard(t, size[0], size[1])
		for i := 0; i < 12; i++ {
			g.feed(toolOn("SHOP-1", at(3, i), "s-b", "git commit -m "+cutTitle(60), exit(0)))
		}
		check(fmt.Sprintf("activity %v", size), "Activity", g.tabTo("Activity"))
	}
	check("main", "Ready", cutRig(t, 80, 24, 60).onRow("JIG-1"))
	check("a phase list", "Slices", bigSlices(t, 80, 24, 3, 200).tabTo("Slices").press("down", "down"))
}

// TestCollapsedListsTakeWhatTheCursorsListLeaves: a list the cursor is not in,
// collapsed to its edges, draws its first row when the cursor's list leaves the
// lines for it, instead of leaving them blank.
func TestCollapsedListsTakeWhatTheCursorsListLeaves(t *testing.T) {
	t.Parallel()
	g := reviewFixRound(t, 80, 24)
	frame := stripANSI(g.view())
	rounds := panelText(t, frame, "Rounds")
	if !strings.Contains(rounds, "round 2") {
		t.Errorf("the Rounds list is a bare box with lines left over:\n%s", frame)
	}
	if strings.Contains(frame, "\n\n\n") {
		t.Errorf("lines are left blank above the keys:\n%s", frame)
	}
}

// TestANamesRowThatCannotFitIsLeftOut: a list draws its names only when every
// name fits the column it names; short of room the names are none, and no column
// is held wide for them.
func TestANamesRowThatCannotFitIsLeftOut(t *testing.T) {
	t.Parallel()
	cols := []column{{}, {gap: 1}, {free: true, gap: 2}}
	rows := [][]string{
		{"", "identifier-name-long", "title"},
		{"●", "JIG-1234567", strings.Repeat("word ", 12)},
	}
	wide := layoutMarked(cols, rows, 100, wrapRows, nil, true)
	if len(wide[0].lines) == 0 || !strings.Contains(wide[0].lines[0], "identifier-name-long") {
		t.Errorf("the names are left out at 100 wide: %q", wide[0].lines)
	}
	narrow := layoutMarked(cols, rows, 36, wrapRows, nil, true)
	if len(narrow[0].lines) != 0 {
		t.Errorf("the names are drawn although one is cut: %q", narrow[0].lines)
	}
	if got := narrow[1].lines[0]; !strings.Contains(got, "JIG-1234567") {
		t.Errorf("the id is cut to keep a name: %q", got)
	}
}

// TestAnExpansionStaysInItsPaneWhateverHasTheFocus: a row expanded in one pane
// is drawn into that pane's folded room when the focus goes to another pane,
// one of text too: the frame above the keys outside the pane is the folded one.
func TestAnExpansionStaysInItsPaneWhateverHasTheFocus(t *testing.T) {
	t.Parallel()
	body := func(frame, pane string) []string {
		lines := strings.Split(frame, "\n")
		for i := len(lines) - 1; i > 0; i-- {
			if strings.HasPrefix(lines[i], "───") {
				lines = lines[:i]
				break
			}
		}
		return outsideFrame(strings.Join(lines, "\n"), pane)
	}
	for _, size := range [][2]int{{60, 20}, {80, 24}, {100, 30}} {
		g := sized(longChartRig(t, 60), size[0], size[1]).openChart("checkout").onRow("JIG-8")
		g.tabTo("Tickets")
		g.press("space")
		g.press("tab")
		after := stripANSI(g.view())
		// What the same keys draw with nothing expanded.
		f := sized(longChartRig(t, 60), size[0], size[1]).openChart("checkout").onRow("JIG-8")
		f.tabTo("Tickets")
		f.press("tab")
		folded := body(stripANSI(f.view()), "Tickets")
		if got := body(after, "Tickets"); strings.Join(got, "\n") != strings.Join(folded, "\n") {
			t.Errorf("%dx%d: the frame outside Tickets moved when the focus left it:\n--- folded\n%s\n--- now\n%s", size[0], size[1], strings.Join(folded, "\n"), after)
		}
		// The pane that lost the focus draws no part of the row without its id.
		if tickets := wholePaneLines(after, "Tickets"); strings.Contains(tickets, "w013") && !strings.Contains(tickets, "JIG-8") {
			t.Errorf("%dx%d: Tickets draws the middle of JIG-8's title without its id:\n%s", size[0], size[1], after)
		}
		// A pane draws expanded the row its own cursor stands on.
		up := sized(longChartRig(t, 60), size[0], size[1]).openChart("checkout").onRow("JIG-8")
		up.tabTo("Tickets")
		up.press("space", "up", "tab")
		none := sized(longChartRig(t, 60), size[0], size[1]).openChart("checkout").onRow("JIG-8")
		none.tabTo("Tickets")
		none.press("up", "tab")
		if got, want := wholePaneLines(stripANSI(up.view()), "Tickets"), wholePaneLines(stripANSI(none.view()), "Tickets"); got != want {
			t.Errorf("%dx%d: Tickets is not the folded one after the cursor left the expanded row:\n%s\n--- folded\n%s", size[0], size[1], got, want)
		}
	}
}

// TestASliceScreenOpenedAgainExpandsWhatSpaceNames: an expansion belongs to the
// visit to a screen: a slice screen opened again starts with nothing expanded, and
// space on a pane expands that pane, not the one the earlier visit expanded.
func TestASliceScreenOpenedAgainExpandsWhatSpaceNames(t *testing.T) {
	t.Parallel()
	g := buildRig(t, 80, 40, 40)
	g.feed(toolOn("BIG-1", at(1, 30), "s-builder", "git commit -m "+cutTitle(60), exit(0)))
	g.press("enter")
	if g.m.Screen() != "slice" {
		t.Fatalf("on %s, want the slice screen", g.m.Screen())
	}
	g.tabTo("Timeline")
	timelineFolded := paneBlock(t, stripANSI(g.view()), "Timeline")
	g.press("space")
	if got := paneBlock(t, stripANSI(g.view()), "Timeline"); got == timelineFolded {
		t.Fatalf("space did not expand the Timeline:\n%s", got)
	}
	g.press("esc", "enter")
	if g.m.Screen() != "slice" || expandedIn(g) != 0 {
		t.Fatalf("a slice screen opened again holds %d expansions on %s, want none", expandedIn(g), g.m.Screen())
	}
	g.tabTo("Calls")
	g.press("space")
	open := stripANSI(g.view())
	if calls := paneBlock(t, open, "Calls"); strings.Contains(calls, "…") || !strings.Contains(calls, "w060") {
		t.Errorf("space on Calls did not expand Calls:\n%s", open)
	}
	if got := paneBlock(t, open, "Timeline"); got != timelineFolded {
		t.Errorf("the Timeline is not the folded one:\n%s\n--- folded\n%s", got, timelineFolded)
	}
}

// TestAScreenOpenedAfreshHasNothingExpanded: opening a screen afresh (a push, a
// sibling key, a step key) starts with nothing expanded, and going back with esc
// finds the screen below as it was left.
func TestAScreenOpenedAfreshHasNothingExpanded(t *testing.T) {
	t.Parallel()
	g := cutRig(t, 80, 40, 20).onRow("JIG-1")
	g.press("space")
	if expandedIn(g) != 1 {
		t.Fatalf("space did not expand a row of the project's list")
	}
	g.press("enter")
	if g.m.Screen() == "main" || expandedIn(g) != 0 {
		t.Fatalf("a screen pushed holds %d expansions on %s, want none", expandedIn(g), g.m.Screen())
	}
	g.press("esc")
	if g.m.Screen() != "main" || expandedIn(g) != 1 {
		t.Errorf("going back finds %d expansions on %s, want the 1 it had", expandedIn(g), g.m.Screen())
	}
}

// TestACutRowsHolderIsStampedOnEveryLine: every line of a row, its continuation
// rows and the lines under it, carries the stamp of the holder it stands for, and
// a line the cursor cannot stand on takes the stamp of the row above.
func TestACutRowsHolderIsStampedOnEveryLine(t *testing.T) {
	t.Parallel()
	g := sized(longChartRig(t, 60), 100, 60).openChart("checkout").onRow("JIG-8")
	pg := g.m.page()
	pane, id, rows, lines := "", "", 0, 0
	for _, l := range pg.lines {
		switch {
		case l.box == boxTop:
			pane, id = l.text, ""
		case l.box == boxBottom || l.names:
			id = ""
		case l.ref != nil:
			id = l.ref.identity().id
			rows++
			if !l.held || l.holder.pane != pane || l.holder.id.id != id {
				t.Errorf("the row %q in %q is stamped %+v", id, pane, l.holder)
			}
		case l.box == boxMid && id != "" && pane == "Tickets":
			lines++
			if !l.held || l.holder.id.id != id {
				t.Errorf("a line of %q in %q (%q) is stamped %+v", id, pane, l.text, l.holder)
			}
		}
	}
	if rows == 0 || lines == 0 {
		t.Fatalf("the chart draws %d rows and %d lines under them", rows, lines)
	}
}

// wholePaneLines is the lines of the pane titled title in frame, whole panes only.
func wholePaneLines(frame, title string) string {
	in := false
	var out []string
	for _, l := range strings.Split(frame, "\n") {
		if strings.Contains(l, "╭─ "+title) {
			in = true
		}
		if in {
			out = append(out, l)
		}
		if in && strings.Contains(l, "╰") {
			break
		}
	}
	return strings.Join(out, "\n")
}

// stampedUnderRows walks the page the last frame drew and says, for every line
// of a pane that stands under a row, whether it carries that row's stamp; it
// gives back the lines that do not, and how many were checked.
func stampedUnderRows(pg page) (bad []string, checked int) {
	pane, cur := "", ""
	for _, l := range pg.lines {
		switch {
		case l.box == boxTop:
			pane, cur = l.text, ""
			continue
		case l.box == boxBottom || l.box == boxNone || l.names:
			cur = ""
			continue
		case l.ref != nil:
			cur = l.ref.identity().id
		}
		if cur == "" || len(l.hiddenRefs) > 0 {
			continue
		}
		checked++
		if !l.held || l.holder.pane != pane || l.holder.id.id != cur {
			bad = append(bad, pane+": "+stripANSI(l.text))
		}
	}
	return bad, checked
}

// TestEveryLineAPaneDrawsCarriesItsHolder: on the ticket, review, brief and chart
// screens at 80x44 and 100x30 every line a pane draws under a row carries that
// row's stamp, and a click on it puts the cursor on that row.
func TestEveryLineAPaneDrawsCarriesItsHolder(t *testing.T) {
	t.Parallel()
	screens := map[string]func(w, h int) *rig{
		"ticket": func(w, h int) *rig { return askRig(t, w, h, askOne, askTwo) },
		"review": func(w, h int) *rig { return reviewFixRound(t, w, h) },
		"brief":  func(w, h int) *rig { return briefRigWith(t, w, h, statusJIG1Brief(), briefTextJIG1()) },
		"chart":  func(w, h int) *rig { return sized(longChartRig(t, 30), w, h).openChart("checkout") },
		"finding": func(w, h int) *rig {
			g := threeRounds(t, w, h, 0)
			g.cursorOn(rowRound, "1").cursorOn(rowFinding, "r1-f1")
			return g.press("enter")
		},
		"slice": func(w, h int) *rig {
			g := threeRoundsWith(t, w, h, longGoalStatus())
			return g.onSlice("fix-1-alpha")
		},
	}
	for name, mk := range screens {
		for _, size := range [][2]int{{80, 44}, {100, 30}} {
			g := mk(size[0], size[1])
			g.view()
			pg := g.m.shown.pg
			if bad, n := stampedUnderRows(pg); n == 0 || len(bad) > 0 {
				t.Errorf("%s %v: %d lines checked, these carry no stamp of their row:\n%s", name, size, n, strings.Join(bad, "\n"))
			}
			// A click on a line under a row selects that row.
			h := g.m.hit
			if h == nil {
				t.Fatalf("%s %v: no hit map", name, size)
			}
			var lines []int
			for line := range h.holder {
				if _, ok := h.rowOf[h.holder[line]]; ok && !isCountMark(g.m.shown.pg.lines[line].text) && h.held[line] && h.sel[line] < 0 && line >= h.start && h.top+line-h.start < h.top+h.rows {
					lines = append(lines, line)
				}
			}
			for _, line := range lines {
				c := mk(size[0], size[1])
				c.view()
				k := c.m.hit
				if line >= len(k.holder) {
					continue
				}
				sel := k.rowOf[k.holder[line]]
				leftClick(c, 4, k.top+line-k.start)
				if got := c.m.top().sel; got != sel {
					t.Errorf("%s %v: a click on line %d (%+v) selected row %d, want %d", name, size, line, c.m.shown.pg.lines[line], got, sel)
				}
			}
		}
	}
}

// TestAClickOnAnAsksTextSelectsTheAsk: the lines of an ask's text in the Needs
// you pane are the ask's own: a click on one puts the cursor on that ask.
func TestAClickOnAnAsksTextSelectsTheAsk(t *testing.T) {
	t.Parallel()
	g := askRig(t, 80, 44, askOne, askTwo)
	g.view()
	h := g.m.hit
	want := -1
	for line, k := range h.holder {
		if k.id.id == "r1-f2" && h.sel[line] < 0 && h.held[line] {
			y := h.top + line - h.start
			leftClick(g, 4, y)
			want = line
			break
		}
	}
	if want < 0 {
		t.Fatalf("the second ask has no text line:\n%s", g.view())
	}
	if r := g.m.refIn(g.m.page()); r == nil || r.id != "r1-f2" {
		t.Errorf("a click on the second ask's text selected %+v", r)
	}
}

// isCountMark says a line is the count of the rows a pane leaves out ("↑ 3 more",
// "↓ 16 older"): it stands for rows, not for the row above it.
func isCountMark(text string) bool {
	t := strings.TrimSpace(stripANSI(text))
	return strings.HasPrefix(t, "↑") || strings.HasPrefix(t, "↓")
}
