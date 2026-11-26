package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// paneTitle is the top edge of the panel titled title in the frame.
func paneTitle(frame, title string) string {
	for _, l := range strings.Split(frame, "\n") {
		if strings.Contains(l, "╭─ "+title+" ") {
			return l
		}
	}
	return ""
}

// typeText types text at the search line, one rune a key.
func typeText(g *rig, text string) {
	g.t.Helper()
	for _, r := range text {
		g.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// TestSlashNarrowsTheFocusedPane: / on a pane of rows opens a search line that
// narrows that pane by the words of its rows, counts what it kept in the title,
// moves the cursor over them and opens the one it is on; the other panes are as
// they were.
func TestSlashNarrowsTheFocusedPane(t *testing.T) {
	t.Parallel()
	// The chart's Tickets: a pane whose rows are all built.
	g := chartScreenRig(t)
	g.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	g.view()
	g.press("/")
	typeText(g, "coupon")
	v := g.view()
	title := paneTitle(v, "Tickets")
	if !strings.Contains(title, "4 of 5") {
		t.Errorf("the Tickets title does not count the rows kept: %q\n%s", title, v)
	}
	for _, gone := range []string{"JIG-8"} {
		if strings.Contains(v, "│"+"▸ ◐ "+gone) || strings.Contains(strings.Join(panelLines(strings.Split(v, "\n"), "Tickets"), "\n"), gone) {
			t.Errorf("the pane still has %s, which no word of is coupon:\n%s", gone, v)
		}
	}
	if got := g.m.selected(); got == nil || got.kind != rowTicket {
		t.Fatalf("the cursor is on %+v, want a ticket that matches", got)
	}
	typeText(g, " apply") // the line is open: a second term narrows further
	v = g.view()
	if !strings.Contains(paneTitle(v, "Tickets"), "1 of 5") {
		t.Errorf("two terms do not narrow to one row:\n%s", v)
	}
	if got := g.m.selected(); got == nil || got.id != "JIG-5" {
		t.Fatalf("the cursor is on %+v, want JIG-5", got)
	}
	if !strings.Contains(v, "╭─ Detail") || !strings.Contains(v, "╭─ Run") {
		t.Errorf("the other panes are not drawn as they were:\n%s", v)
	}
	g.send(tea.KeyMsg{Type: tea.KeyEnter})
	if g.m.Screen() != "ticket" || g.m.top().ticket != "JIG-5" {
		t.Errorf("enter opens %s %q, want JIG-5's screen", g.m.Screen(), g.m.top().ticket)
	}

	// The ticket screen's Activity, a pane that draws a window of its rows: the
	// search reads every one of them.
	a := slicesRig(t, 9, 6, 80, 24)
	a.tabTo("Activity")
	all := len(a.m.page().windowed["Activity"])
	a.press("/")
	typeText(a, "s3")
	v = a.view()
	if got := a.m.page().windowed["Activity"]; len(got) == 0 || len(got) >= all {
		t.Fatalf("the pane keeps %d of %d lines for s3", len(got), all)
	}
	for _, r := range a.m.page().windowed["Activity"] {
		if r.slice != "s3" {
			t.Errorf("the pane kept the line %+v, which is not about s3", r)
		}
	}
	if !strings.Contains(paneTitle(v, "Activity"), " of "+strconv.Itoa(all)) {
		t.Errorf("the Activity title does not count the kept lines of %d: %q", all, paneTitle(v, "Activity"))
	}
	a.send(tea.KeyMsg{Type: tea.KeyEnd})
	last := a.m.selected()
	full := a.m.page().windowed["Activity"]
	if last == nil || last.id != full[len(full)-1].id {
		t.Errorf("end reaches %+v, want the last line kept, %+v", last, full[len(full)-1])
	}
	a.send(tea.KeyMsg{Type: tea.KeyEnter})
	if a.m.Screen() != "slice" || a.m.top().slice != "s3" {
		t.Errorf("enter on a kept line opens %s %q, want the slice s3", a.m.Screen(), a.m.top().slice)
	}
}

// TestEscClearsThePaneSearchAndKeepsTheCursor: esc ends the search, the pane has
// all its rows again, and its cursor is on the row it stood on before.
func TestEscClearsThePaneSearchAndKeepsTheCursor(t *testing.T) {
	t.Parallel()
	g := slicesRig(t, 9, 6, 80, 26)
	g.tabTo("Activity")
	g.press("down", "down", "down")
	was := g.m.selected()
	if was == nil || was.kind != rowLog {
		t.Fatalf("the cursor is on %+v", was)
	}
	all := len(g.m.page().windowed["Activity"])
	g.press("/")
	typeText(g, "s2")
	if got := g.m.selected(); got == nil || got.slice != "s2" {
		t.Fatalf("the cursor is on %+v, want a line of s2", got)
	}
	g.send(tea.KeyMsg{Type: tea.KeyEsc})
	if g.m.top().psearch.applied() || len(g.m.page().windowed["Activity"]) != all {
		t.Errorf("esc leaves the pane narrowed: %d of %d lines", len(g.m.page().windowed["Activity"]), all)
	}
	if got := g.m.selected(); got == nil || *got != *was {
		t.Errorf("esc puts the cursor on %+v, want the line it stood on, %+v", got, was)
	}
	if g.m.Screen() != "ticket" {
		t.Errorf("esc went back to %s", g.m.Screen())
	}

	// After enter the query stays and the line is closed: back from the slice the
	// pane is still narrowed, and esc widens it before it goes back.
	g.press("/")
	typeText(g, "s2")
	g.send(tea.KeyMsg{Type: tea.KeyEnter})
	g.press("esc")
	g.view()
	if g.m.Screen() != "ticket" || !g.m.top().psearch.applied() || g.m.top().psearch.open {
		t.Fatalf("back from the slice: %s, narrowed %v, line open %v; want the ticket, narrowed, line closed", g.m.Screen(), g.m.top().psearch.applied(), g.m.top().psearch.open)
	}
	g.press("esc")
	g.view()
	if g.m.Screen() != "ticket" || g.m.top().psearch.applied() {
		t.Fatalf("esc after a search: %s, narrowed %v; want the ticket, widened", g.m.Screen(), g.m.top().psearch.applied())
	}
}

// TestSlashOnATextPaneSaysWhy: a pane of text has no rows to narrow, and its key
// row says so.
func TestSlashOnATextPaneSaysWhy(t *testing.T) {
	t.Parallel()
	g := chartScreenRig(t)
	g.tabTo("Run")
	g.press("/")
	if g.m.top().psearch.open || g.m.notice != "Run is text: it has no rows to search" {
		t.Errorf("/ on a pane of text: line open %v, notice %q", g.m.top().psearch.open, g.m.notice)
	}
	g.press("?")
	words := readWords(g.view())
	if !strings.Contains(words, "/ search in Run not now: Run is text: it has no rows to search") {
		t.Errorf("? does not give the reason on the row of /:\n%s", words)
	}
}
