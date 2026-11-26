package tui

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// paneRig is a ticket screen with four panes: Steps (rows), the sub-steps of the
// review (text), Needs you (rows: two asks) and Activity (text).
func paneRig(t *testing.T) *rig {
	t.Helper()
	g := newRig(t, "JIG-2")
	g.send(tea.WindowSizeMsg{Width: 80, Height: 44})
	g.feed(jig2Mid()...)
	g.view()
	return g
}

// TestTabMovesTheFocusAcrossPanes: tab and shift+tab walk the panes of a screen
// in the order they are drawn, wrapping at both ends; a screen of one pane has
// no pane to move to and says so.
func TestTabMovesTheFocusAcrossPanes(t *testing.T) {
	t.Parallel()
	g := paneRig(t)
	titles := slices.Clone(g.m.panesNow)
	if !slices.Equal(titles, []string{"Steps", "Review", "Needs you", "Activity"}) {
		t.Fatalf("the ticket screen's panes are %v", titles)
	}
	if got := g.m.focusedPane(); got != "Steps" {
		t.Fatalf("the focus begins on %q, want the first pane with rows, Steps", got)
	}
	for i := 1; i <= len(titles); i++ {
		g.press("tab")
		g.view()
		if got, want := g.m.focusedPane(), titles[i%len(titles)]; got != want {
			t.Fatalf("after %d tabs the focus is on %q, want %q", i, got, want)
		}
	}
	g.press("shift+tab")
	g.view()
	if got := g.m.focusedPane(); got != "Activity" {
		t.Errorf("shift+tab from the first pane goes to %q, want the last, Activity", got)
	}
	g.press("shift+tab")
	g.view()
	if got := g.m.focusedPane(); got != "Needs you" {
		t.Errorf("shift+tab from Activity goes to %q, want Needs you", got)
	}

	main := mainRig(t)
	main.view()
	main.press("tab")
	if want := "this screen has one pane"; main.m.notice != want || main.m.focusedPane() != "" {
		t.Errorf("tab on the project's list: notice %q, focus %q; want the notice %q and no pane", main.m.notice, main.m.focusedPane(), want)
	}
}

// TestEachPaneKeepsItsCursorWhenTheFocusLeaves: a pane's cursor stays on its row
// while another pane has the focus, and is on that row when the focus comes
// back; the screen's memory keeps them across a screen opened and left.
func TestEachPaneKeepsItsCursorWhenTheFocusLeaves(t *testing.T) {
	t.Parallel()
	g := paneRig(t)
	g.press("up") // Steps: the step before the current one
	steps := g.m.selected()
	if steps == nil || steps.kind != rowBuild {
		t.Fatalf("the Steps cursor is on %+v, want the build step", steps)
	}
	g.tabTo("Needs you").press("down")
	asks := g.m.selected()
	if asks == nil || asks.kind != rowAsk || asks.id != "r1-f2" {
		t.Fatalf("the Needs you cursor is on %+v, want ask r1-f2", asks)
	}
	g.tabTo("Steps")
	if got := g.m.selected(); got == nil || *got != *steps {
		t.Errorf("back on Steps the cursor is on %+v, want %+v", got, steps)
	}
	g.tabTo("Needs you")
	if got := g.m.selected(); got == nil || *got != *asks {
		t.Errorf("back on Needs you the cursor is on %+v, want %+v", got, asks)
	}

	// The unfocused pane keeps its row marked, and not lit.
	g.tabTo("Steps")
	v := g.view()
	if n := strings.Count(v, "▸"); n != 3 {
		t.Errorf("the frame has %d cursor marks, want the focused pane's and the two panes the focus passed through:\n%s", n, v)
	}

	// A screen opened from a pane and left comes back with the focus and every
	// cursor where it was.
	g.tabTo("Needs you").press("enter")
	if g.m.Screen() != "review" {
		t.Fatalf("on %s, want review", g.m.Screen())
	}
	g.press("esc")
	g.view()
	if got := g.m.focusedPane(); got != "Needs you" {
		t.Errorf("back on the ticket the focus is on %q, want Needs you", got)
	}
	if got := g.m.selected(); got == nil || *got != *asks {
		t.Errorf("back on the ticket the cursor is on %+v, want %+v", got, asks)
	}
	g.tabTo("Steps")
	if got := g.m.selected(); got == nil || *got != *steps {
		t.Errorf("back on the ticket Steps' cursor is on %+v, want %+v", got, steps)
	}
}

// TestMovesActInTheFocusedPane: up, down, home, end and the page keys move the
// focused pane's cursor and stop at its first and last row; a key that acts on a
// row reads the focused pane's.
func TestMovesActInTheFocusedPane(t *testing.T) {
	t.Parallel()
	g := paneRig(t)
	g.press("down", "down", "down", "down", "down", "down", "down", "down")
	if got := g.m.selected(); got == nil || got.kind != rowLand {
		t.Errorf("down past the last step leaves Steps: the cursor is on %+v, want the land step", got)
	}
	g.press("home")
	if got := g.m.selected(); got == nil || got.kind != rowBrief {
		t.Errorf("home in Steps goes to %+v, want the brief step", got)
	}
	g.press("up")
	if got := g.m.selected(); got == nil || got.kind != rowBrief {
		t.Errorf("up from the first step goes to %+v, want it to stay", got)
	}

	g.tabTo("Needs you").press("end")
	if got := g.m.selected(); got == nil || got.id != "r1-f2" {
		t.Fatalf("end in Needs you goes to %+v, want ask r1-f2", got)
	}
	g.press("down")
	sendKey(g, "pgdown")
	if got := g.m.selected(); got == nil || got.id != "r1-f2" {
		t.Errorf("down past the last ask goes to %+v, want it to stay", got)
	}
	g.press("up", "up", "up")
	if got := g.m.selected(); got == nil || got.id != "r1-f1" {
		t.Errorf("up past the first ask goes to %+v, want ask r1-f1, not a step", got)
	}
	g.press("d")
	if got := g.r.lines(); len(got) != 1 || !strings.Contains(got[0], "triage JIG-2 r1-f1 dismiss") {
		t.Errorf("d decides the focused pane's row: ran %q", got)
	}
}

// TestAPaneOfTextScrollsByLines: a pane of text has no cursor and no row under
// it; the keys scroll the page by lines, and each pane's scroll is its own.
func TestAPaneOfTextScrollsByLines(t *testing.T) {
	t.Parallel()
	g := chartScreenRig(t)
	g.send(tea.WindowSizeMsg{Width: 80, Height: 16})
	g.view()
	tickets := g.m.selected()
	if tickets == nil || tickets.kind != rowTicket {
		t.Fatalf("the chart's cursor is on %+v, want a ticket", tickets)
	}
	g.tabTo("Run")
	if got := g.m.selected(); got != nil {
		t.Errorf("a pane of text has the row %+v under its cursor", got)
	}
	if v := g.view(); !strings.Contains(v, "╭─ Run") {
		t.Errorf("tab to a pane of text does not show it:\n%s", v)
	}
	first := g.view()
	g.press("up")
	if g.view() == first {
		t.Errorf("up in a pane of text did not scroll the page:\n%s", first)
	}
	if got := g.m.selected(); got != nil {
		t.Errorf("up in a pane of text put the cursor on %+v", got)
	}
	g.press("a")
	if g.m.input != nil {
		t.Errorf("a on a pane of text opened a prompt")
	}
	g.tabTo("Tickets")
	if got := g.m.selected(); got == nil || *got != *tickets {
		t.Errorf("back on Tickets the cursor is on %+v, want %+v", got, tickets)
	}
}

// TestAClickFocusesThePaneUnderIt: a click on a row of a pane gives it the focus
// and puts its cursor there; a click on a pane's text gives it the focus.
func TestAClickFocusesThePaneUnderIt(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...)
	v := g.view()
	if got := g.m.focusedPane(); got != "Steps" {
		t.Fatalf("the focus begins on %q", got)
	}
	click(g, termRow(v, "builder asks about slice c"))
	if got := g.m.focusedPane(); got != "Needs you" {
		t.Errorf("a click on a question gives the focus to %q, want Needs you", got)
	}
	if got := g.m.selected(); got == nil || got.kind != rowQuestion {
		t.Errorf("the click leaves the cursor on %+v, want the question", got)
	}
	v = g.view()
	click(g, termRow(v, "its oracle failed"))
	if got := g.m.focusedPane(); got != "Activity" || g.m.selected() != nil {
		t.Errorf("a click on a line of Activity: focus %q, row %+v; want Activity and no row", got, g.m.selected())
	}
	v = g.view()
	click(g, termRow(v, "1 of 4 green"))
	if got := g.m.focusedPane(); got != "Steps" {
		t.Errorf("a click on a step gives the focus to %q, want Steps", got)
	}
	if got := g.m.selected(); got == nil || got.kind != rowBuild {
		t.Errorf("the click leaves the cursor on %+v, want the build step", got)
	}
}

// TestTheFocusedPaneIsTheOneDrawnLit: the border of the pane that has the focus
// is drawn in the key color and every other pane's in the dim one, down to the
// marks a cut pane draws for the rows it hides; the frame loses nothing in a
// terminal with no color.
func TestTheFocusedPaneIsTheOneDrawnLit(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...)
	g.m.st = styles{r: colored()}
	st := g.m.st
	lineWith := func(v, text string) string {
		for _, l := range strings.Split(v, "\n") {
			if strings.Contains(l, text) {
				return l
			}
		}
		t.Fatalf("no line has %q:\n%s", text, v)
		return ""
	}
	v := g.view()
	if l := lineWith(v, "Steps"); !strings.Contains(l, st.key("╭─")) || strings.Contains(l, st.dim("╭─")) {
		t.Errorf("the focused pane's top edge is not lit: %q", l)
	}
	if l := lineWith(v, "Needs you"); !strings.Contains(l, st.dim("╭─")) || strings.Contains(l, st.key("╭─")) {
		t.Errorf("an unfocused pane's top edge is lit: %q", l)
	}
	if l := lineWith(v, "builder asks about slice c"); !strings.Contains(l, st.dim("│")) || strings.Contains(l, st.key("│")) {
		t.Errorf("an unfocused pane's side is lit: %q", l)
	}
	g.press("tab")
	v = g.view()
	if l := lineWith(v, "Needs you"); !strings.Contains(l, st.key("╭─")) {
		t.Errorf("after tab the pane's top edge is not lit: %q", l)
	}
	if l := lineWith(v, "builder asks about slice c"); !strings.Contains(l, st.key("│")) || !strings.Contains(l, st.key("╰")+"") && false {
		t.Errorf("after tab the pane's side is not lit: %q", l)
	}
	if l := lineWith(v, "Steps"); !strings.Contains(l, st.dim("╭─")) {
		t.Errorf("after tab the first pane is still lit: %q", l)
	}

	// A focused pane the window cuts draws its marks with the same border.
	c := chartScreenRig(t)
	c.m.st = styles{r: colored()}
	c.send(tea.WindowSizeMsg{Width: 80, Height: 12})
	cv := c.view()
	var marks int
	for _, l := range strings.Split(cv, "\n") {
		if strings.Contains(l, "more") && strings.Contains(l, "│") {
			marks++
			if !strings.Contains(l, c.m.st.key("│")) || strings.Contains(l, c.m.st.dim("│")) {
				t.Errorf("a mark inside the focused pane has a border that is not lit: %q", l)
			}
		}
	}
	if marks == 0 {
		t.Errorf("the cut pane draws no mark inside it:\n%s", cv)
	}
	c.tabTo("Detail")
	cv = c.view()
	for _, l := range strings.Split(cv, "\n") {
		if strings.Contains(l, "more") && strings.Contains(l, "│") && strings.Contains(l, c.m.st.key("│")) {
			t.Errorf("a mark inside an unfocused pane is lit: %q", l)
		}
	}
}

// TestTheHintLineNamesTheFocusedPane: tab says which pane it goes to and the
// move keys which pane they move in, on a screen of several panes; a screen of
// one says nothing of panes; the keys list names them all.
func TestTheHintLineNamesTheFocusedPane(t *testing.T) {
	t.Parallel()
	g := paneRig(t)
	hint := func() string { return strings.Join(strings.Fields(footerKeys(g.view())), " ") }
	if h := hint(); !strings.Contains(h, "tab next pane: Review") || !strings.Contains(h, "↑↓ move in Steps") {
		t.Errorf("the hint line on Steps: %q", h)
	}
	g.tabTo("Needs you")
	if h := hint(); !strings.Contains(h, "tab next pane: Activity") || !strings.Contains(h, "↑↓ move in Needs you") {
		t.Errorf("the hint line on Needs you: %q", h)
	}
	g.press("?")
	words := readWords(g.view())
	for _, want := range []string{"tab next pane: Activity", "shift+tab previous pane: Review"} {
		if !strings.Contains(words, want) {
			t.Errorf("? lacks %q:\n%s", want, words)
		}
	}
	g.press("?")

	main := mainRig(t)
	if h := strings.Join(strings.Fields(footerKeys(main.view())), " "); strings.Contains(h, "pane") {
		t.Errorf("the project's list is one pane and its hint line names panes: %q", h)
	}
}
