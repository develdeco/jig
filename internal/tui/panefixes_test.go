package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestALineThatComesInLeavesTheFocusOnItsPane: a line that arrives while a pane
// of text has the focus does not hand the focus to the pane the cursor's last row
// is in; only a key, a click or the follow moves it.
func TestALineThatComesInLeavesTheFocusOnItsPane(t *testing.T) {
	t.Parallel()
	for _, h := range []int{60, 80} {
		g := chartScreenRig(t)
		g.send(tea.WindowSizeMsg{Width: 230, Height: h})
		g.tabTo("Activity")
		g.press("down", "down")
		g.press("shift+tab")
		g.view()
		if got := g.m.focusedPane(); got != "Run" {
			t.Fatalf("%d: shift+tab from Activity goes to %q, want Run", h, got)
		}
		g.feed(jl(at(30, 0), "JIG-4", "", "publish-done"))
		g.view()
		g.view()
		if got := g.m.focusedPane(); got != "Run" {
			t.Errorf("%d: a line that came in moved the focus to %q, want Run", h, got)
		}
	}
}

// TestTheNarrowedPanesAreFollowedByTheirRows: what follows the Tickets or Steps
// cursor follows the row it stands on when `/` has narrowed the pane, not its
// place among the rows kept.
func TestTheNarrowedPanesAreFollowedByTheirRows(t *testing.T) {
	t.Parallel()
	g := chartScreenRig(t)
	g.send(tea.WindowSizeMsg{Width: 100, Height: 45})
	g.tabTo("Tickets")
	g.press("/")
	typeText(g, "receipts")
	v := g.view()
	if got := g.m.selected(); got == nil || got.id != "JIG-7" {
		t.Fatalf("the cursor is on %+v, want JIG-7", got)
	}
	if d := paneTitle(v, "Detail"); !strings.Contains(d, "JIG-7") {
		t.Errorf("the Detail pane of a narrowed Tickets is %q, want it about JIG-7", d)
	}

	s := paneRig(t)
	s.tabTo("Steps")
	s.press("/")
	typeText(s, "asks")
	sv := s.view()
	if got := s.m.selected(); got == nil || got.kind != rowReview {
		t.Fatalf("the Steps cursor is on %+v, want the review step", got)
	}
	if !strings.Contains(sv, "╭─ Review") {
		t.Errorf("the sub-steps of the review are not drawn under a narrowed Steps:\n%s", sv)
	}
}

// TestZShowsATextPaneWholeNotAsTheScreenCutIt: the chart's Run and Detail, which the
// allocator cuts to share a short screen, are built whole when zoomed.
func TestZShowsATextPaneWholeNotAsTheScreenCutIt(t *testing.T) {
	t.Parallel()
	g := chartScreenRig(t)
	g.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	g.tabTo("Run")
	cut := strings.Join(panelLines(strings.Split(g.view(), "\n"), "Run"), "\n")
	g.press("z")
	whole := strings.Join(panelLines(strings.Split(g.view(), "\n"), "Run"), "\n")
	if len(whole) <= len(cut) || !strings.Contains(whole, "ends at") {
		t.Errorf("the zoomed Run is no more than the screen drew:\n%s\nwas:\n%s", whole, cut)
	}
	g.press("z")
	g.tabTo("Detail")
	cut = strings.Join(panelLines(strings.Split(g.view(), "\n"), "Detail"), "\n")
	g.press("z")
	if whole = strings.Join(panelLines(strings.Split(g.view(), "\n"), "Detail"), "\n"); len(whole) < len(cut) {
		t.Errorf("the zoomed Detail is less than the screen drew:\n%s\nwas:\n%s", whole, cut)
	}
}

// TestAZoomedRunScrollsToItsLastEnd: a pane of text is read by lines through every
// line it has, whatever the line begins with: a Run that ends at a dozen tickets
// draws the last of them when read down.
func TestAZoomedRunScrollsToItsLastEnd(t *testing.T) {
	t.Parallel()
	g := newRig(t, "")
	lines, status := chartTickets()
	var ends []map[string]any
	for i := 20; i < 32; i++ {
		ends = append(ends, map[string]any{"ticket": fmt.Sprintf("JIG-%d", i), "blocker": "JIG-4", "kind": "merged", "fact": "its PR is open"})
	}
	answerChart(g, chartRoster(checkoutListing()), "checkout", checkoutJSON(func(v map[string]any) { v["ends"] = ends }), status)
	g.feed(lines...)
	g.openChart("checkout")
	g.send(tea.WindowSizeMsg{Width: 80, Height: 16})
	g.tabTo("Run")
	g.press("z")
	var last string
	for i := 0; i < 20; i++ {
		g.press("down")
		last = g.view()
	}
	if !strings.Contains(last, "JIG-31") {
		t.Errorf("reading the zoomed Run down never draws its last end:\n%s", last)
	}
}

// TestUnzoomingShowsNothingNewWhenNoLineCameIn: what a zoom changes in what a
// pane draws is not read as lines that came in.
func TestUnzoomingShowsNothingNewWhenNoLineCameIn(t *testing.T) {
	t.Parallel()
	g := slicesRig(t, 30, 30, 100, 30)
	g.press("enter")
	if g.m.Screen() != "build" {
		t.Fatalf("on %s, want build", g.m.Screen())
	}
	g.press("z")
	for i := 0; i < 14; i++ {
		g.press("down")
	}
	g.press("z")
	v := g.view()
	if strings.Contains(v, " new") || g.m.top().fresh != 0 {
		t.Errorf("unzooming reads a line as new when none came in (fresh %d):\n%s", g.m.top().fresh, v)
	}
}

// TestTheLastRowBringsThePanelsBelowIntoView: a cursor on its panel's last row
// shows the panels that follow as far as the room allows, with no blank lines.
// On a phase screen the Detail of the row comes first, then the panels below it:
// the room the list and the Detail leave is theirs, and a mark says what lies
// below.
func TestTheLastRowBringsThePanelsBelowIntoView(t *testing.T) {
	t.Parallel()
	g := slicesRig(t, 9, 6, 80, 24)
	g.press("enter")
	g.tabTo("Slices").press("end")
	v := g.view()
	if !strings.Contains(v, "╭─ Detail · s9") || !strings.Contains(v, "↓ ") {
		t.Errorf("the cursor on the last slice does not bring its detail into view, with a mark of what is below:\n%s", v)
	}
	roomy := slicesRig(t, 2, 2, 100, 40)
	roomy.press("enter")
	roomy.tabTo("Slices").press("end")
	if view := roomy.view(); !strings.Contains(view, "╭─ Build activity") {
		t.Errorf("the cursor on the last slice does not bring the activity below into view when the room is there:\n%s", view)
	}
	lines := strings.Split(v, "\n")
	for i := 3; i < len(lines)-4; i++ {
		if strings.TrimSpace(lines[i]) == "" && strings.TrimSpace(lines[i+1]) == "" {
			t.Errorf("two blank lines at %d under the cursor's panel:\n%s", i, v)
			break
		}
	}
}

// TestAWindowNeverBeginsOnARowsDetail: under a pane's "↑ n more" mark stands a row's
// first line, never the detail a list draws under a row.
func TestAWindowNeverBeginsOnARowsDetail(t *testing.T) {
	t.Parallel()
	for h := 14; h <= 30; h++ {
		g := newRig(t, "JIG-2")
		g.send(tea.WindowSizeMsg{Width: 80, Height: h})
		g.feed(jig2Mid()...)
		g.press("enter")
		for _, keys := range [][]string{{"end"}, {"tab", "end"}, {"tab", "tab", "end"}} {
			g.press(keys...)
			lines := strings.Split(g.view(), "\n")
			for i := 0; i+1 < len(lines); i++ {
				if strings.Contains(lines[i], "│  ↑ ") && strings.Contains(lines[i], " more") && strings.HasPrefix(lines[i+1], "  │      ") {
					t.Errorf("height %d after %v: a row's detail stands under the mark:\n%s", h, keys, strings.Join(lines, "\n"))
				}
			}
		}
	}
}

// TestASlicesWindowNeverBeginsOnARecordWithItsSliceHidden: on the build screen,
// under the count of the slices above stands a slice's row, never one of the
// records a slice has under it.
func TestASlicesWindowNeverBeginsOnARecordWithItsSliceHidden(t *testing.T) {
	t.Parallel()
	for h := 12; h <= 30; h++ {
		g := newRig(t, "JIG-1")
		g.send(tea.WindowSizeMsg{Width: 80, Height: h})
		g.feed(jig1()...)
		g.press("enter")
		if g.m.Screen() != "build" {
			t.Fatalf("on %s, want build", g.m.Screen())
		}
		for _, keys := range [][]string{{"down"}, {"down"}, {"down"}, {"end"}} {
			g.press(keys...)
			lines := strings.Split(g.view(), "\n")
			for i := 0; i+1 < len(lines); i++ {
				if strings.Contains(lines[i], "│  ↑ ") && strings.Contains(lines[i], " more") && strings.HasPrefix(lines[i+1], "  │     ") {
					t.Errorf("height %d after %v: a slice's record stands under the mark:\n%s", h, keys, strings.Join(lines, "\n"))
				}
			}
		}
	}
}

// TestAPanesCursorMovesInsideItsWindow: a down on a pane that draws a window of its
// rows keeps the newest line in view; the window moves only when the cursor would
// leave it.
func TestAPanesCursorMovesInsideItsWindow(t *testing.T) {
	t.Parallel()
	g := slicesRig(t, 20, 20, 100, 30)
	g.tabTo("Activity")
	g.press("down")
	full := g.m.page().windowed["Activity"]
	if got := g.m.selected(); got == nil || got.id != full[1].id {
		t.Fatalf("the cursor is on %+v, want the second line", got)
	}
	shown := map[string]bool{}
	for _, r := range g.m.page().refs() {
		shown[r.id] = true
	}
	if !shown[full[0].id] {
		t.Errorf("a down hid the newest line: the window began at the cursor\n%s", g.view())
	}
	// Down to the window's edge: the cursor is the last row, and the window moves
	// one row at a time after it.
	var tops []int
	for i := 0; i < 20; i++ {
		g.press("down")
		tops = append(tops, g.m.top().paneCurs["Activity"].top)
	}
	for i := 1; i < len(tops); i++ {
		if tops[i] < tops[i-1] || tops[i]-tops[i-1] > 1 {
			t.Errorf("the window jumped from %d to %d: %v", tops[i-1], tops[i], tops)
		}
	}
	g.press("up")
	if tops2 := g.m.top().paneCurs["Activity"].top; tops2 != tops[len(tops)-1] {
		t.Errorf("a move back inside the window moved it from %d to %d", tops[len(tops)-1], tops2)
	}
}

// TestAKeyOnThePlanBuildsNoRow: a key reads no ticket into a row of a plan of 14
// tasks: a down builds none, and tab and z build one page, the frame's own.
func TestAKeyOnThePlanBuildsNoRow(t *testing.T) {
	t.Parallel()
	var tasks []string
	for i := 1; i <= 14; i++ {
		tasks = append(tasks, fmt.Sprintf("SHOP-%d", i))
	}
	g := planRig(t, planSpec{tasks: tasks})
	g.view()
	for _, k := range []string{"down", "down", "up"} {
		reads, pages := g.m.rowReads, g.m.pageBuilds
		g.press(k)
		if got := g.m.rowReads - reads; got != 0 {
			t.Errorf("a %s built %d rows", k, got)
		}
		if got := g.m.pageBuilds - pages; got > 2 {
			t.Errorf("a %s built %d pages", k, got)
		}
	}
	for _, k := range []string{"tab", "z", "z"} {
		reads, pages := g.m.rowReads, g.m.pageBuilds
		g.press(k)
		if got := g.m.rowReads - reads; got != 0 {
			t.Errorf("%s built %d rows", k, got)
		}
		// The frame builds its page; a second only when the hint line of what the key
		// moved to has another height than the one the page was sized by.
		if got := g.m.pageBuilds - pages; got < 1 || got > 2 {
			t.Errorf("%s built %d pages", k, got)
		}
	}
}

// TestTabOnAOnePaneScreenSaysWhy: a screen with one pane lists tab with the reason
// it cannot run, and never words it as going to a pane.
func TestTabOnAOnePaneScreenSaysWhy(t *testing.T) {
	t.Parallel()
	g := mainRig(t)
	g.view()
	g.press("?")
	words := readWords(g.view())
	if !strings.Contains(words, "tab next pane not now: this screen has one pane") {
		t.Errorf("? does not say why tab cannot run on main:\n%s", words)
	}
	if strings.Contains(words, "next pane: ") {
		t.Errorf("? words tab as going to a pane that is none:\n%s", words)
	}
}
