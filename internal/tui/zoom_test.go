package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// zoomSizes are the terminals the zoom is drawn at.
var zoomSizes = [][2]int{{80, 24}, {100, 30}, {230, 50}}

// TestZShowsTheFocusedPaneWholeAndBack: z draws the focused pane over the whole
// body, with its cursor on its row and all the room for its lines; z draws the
// screen again exactly as it was.
func TestZShowsTheFocusedPaneWholeAndBack(t *testing.T) {
	t.Parallel()
	for _, size := range zoomSizes {
		g := slicesRig(t, 9, 6, size[0], size[1])
		g.tabTo("Activity")
		before := g.view()
		inPane := strings.Count(strings.Join(panelLines(strings.Split(before, "\n"), "Activity"), "\n"), "\n") - 3
		g.press("z")
		zoomed := g.view()
		if strings.Contains(zoomed, "╭─ Steps") || strings.Contains(zoomed, "╭─ Build") {
			t.Fatalf("%v: the zoomed frame still draws the other panes:\n%s", size, zoomed)
		}
		rows := panelLines(strings.Split(zoomed, "\n"), "Activity")
		if len(rows) == 0 || len(strings.Split(zoomed, "\n")) != size[1] {
			t.Fatalf("%v: the zoomed frame has the Activity pane in %d lines of %d:\n%s", size, len(rows), len(strings.Split(zoomed, "\n")), zoomed)
		}
		if got := len(rows) - 3; got <= inPane {
			t.Errorf("%v: the zoomed pane shows %d lines, no more than the %d it had:\n%s", size, got, inPane, zoomed)
		}
		if !strings.Contains(strings.Join(rows[2:4], "\n"), "▸ ") {
			t.Errorf("%v: the cursor is not on the newest line:\n%s", size, zoomed)
		}
		if got := g.m.selected(); got == nil || got.kind != rowLog {
			t.Errorf("%v: the zoomed pane's cursor is on %+v, want a line", size, got)
		}
		g.press("end")
		if v := g.view(); !strings.Contains(v, "▸ ") || g.m.selected() == nil || g.m.selected().id != g.m.page().windowed["Activity"][len(g.m.page().windowed["Activity"])-1].id {
			t.Errorf("%v: end does not reach the oldest line:\n%s", size, v)
		}
		g.press("home", "z")
		if after := g.view(); after != before {
			t.Errorf("%v: z again does not draw the screen as it was:\n%s\nwas:\n%s", size, after, before)
		}
	}

	// A pane of text zooms whole.
	c := chartScreenRig(t)
	c.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	c.tabTo("Detail")
	c.press("z")
	v := c.view()
	if strings.Contains(v, "╭─ Tickets") || !strings.Contains(v, "╭─ Detail") || !strings.Contains(v, "╰") {
		t.Errorf("a pane of text does not zoom whole:\n%s", v)
	}
	if got := c.m.selected(); got != nil {
		t.Errorf("a zoomed pane of text has the row %+v under a cursor", got)
	}
	c.press("z")
	if got := c.m.focusedPane(); got != "Detail" || c.m.top().zoom != "" {
		t.Errorf("z again leaves the focus on %q with zoom %q", got, c.m.top().zoom)
	}
}

// TestEscWhileZoomedOnlyUnzooms: esc draws the screen again and goes no further
// back; once the screen is whole it goes back as it does. The zoom is kept with
// the screen: a line opened from a zoomed pane is left to find the pane zoomed.
func TestEscWhileZoomedOnlyUnzooms(t *testing.T) {
	t.Parallel()
	g := slicesRig(t, 9, 6, 100, 30)
	g.tabTo("Activity")
	whole := g.view()
	g.press("z", "esc")
	if g.m.Screen() != "ticket" || g.m.top().zoom != "" {
		t.Fatalf("esc on a zoomed pane: screen %s, zoom %q; want the ticket, drawn whole", g.m.Screen(), g.m.top().zoom)
	}
	if v := g.view(); v != whole {
		t.Errorf("esc does not draw the screen as it was:\n%s", v)
	}
	g.press("z", "enter")
	if g.m.Screen() != "slice" {
		t.Fatalf("enter on a zoomed line opens %s, want a slice", g.m.Screen())
	}
	g.press("esc")
	if g.m.Screen() != "ticket" || g.m.top().zoom != "Activity" {
		t.Fatalf("back from the slice: screen %s, zoom %q; want the ticket still zoomed on Activity", g.m.Screen(), g.m.top().zoom)
	}
	g.press("esc", "esc")
	if g.m.Screen() != "main" {
		t.Errorf("the second esc goes back to %s, want the project's list", g.m.Screen())
	}
}

// TestTabWhileZoomedMovesTheZoom: tab and shift+tab zoom the next and the
// previous pane, and every other key acts as it does on the pane.
func TestTabWhileZoomedMovesTheZoom(t *testing.T) {
	t.Parallel()
	g := paneRig(t)
	g.press("z")
	if g.m.top().zoom != "Steps" {
		t.Fatalf("z zooms %q, want Steps", g.m.top().zoom)
	}
	g.press("up")
	if got := g.m.selected(); got == nil || got.kind != rowBuild {
		t.Fatalf("up in the zoomed Steps goes to %+v, want the build step", got)
	}
	g.press("tab")
	g.view()
	if g.m.top().zoom != "Build" || g.m.focusedPane() != "Build" {
		t.Fatalf("tab zooms %q with the focus on %q, want Build (the step Steps is on)", g.m.top().zoom, g.m.focusedPane())
	}
	g.tabTo("Needs you")
	if g.m.top().zoom != "Needs you" {
		t.Fatalf("tab to Needs you leaves the zoom on %q", g.m.top().zoom)
	}
	g.press("down")
	if got := g.m.selected(); got == nil || got.id != "r1-f2" {
		t.Errorf("down in the zoomed Needs you goes to %+v, want ask r1-f2", got)
	}
	g.press("shift+tab", "shift+tab")
	g.view()
	if g.m.top().zoom != "Steps" {
		t.Errorf("shift+tab twice zooms %q, want Steps", g.m.top().zoom)
	}
	if got := g.m.selected(); got == nil || got.kind != rowBuild {
		t.Errorf("back on the zoomed Steps the cursor is on %+v, want the build step it was left on", got)
	}
}

// TestTheZoomedPaneIsNamedInTheBreadcrumb: the header says which pane is zoomed,
// and the keys name the way out.
func TestTheZoomedPaneIsNamedInTheBreadcrumb(t *testing.T) {
	t.Parallel()
	g := slicesRig(t, 9, 6, 100, 30)
	g.tabTo("Activity")
	if head := strings.Split(g.view(), "\n")[0]; strings.Contains(head, "zoomed") {
		t.Errorf("the header names a zoom before there is one: %s", head)
	}
	g.press("z")
	v := g.view()
	if head := strings.Split(v, "\n")[0]; !strings.Contains(head, "Activity, zoomed") {
		t.Errorf("the header of a zoomed pane does not name it: %s", head)
	}
	if h := strings.Join(strings.Fields(footerKeys(v)), " "); !strings.Contains(h, "esc unzoom") {
		t.Errorf("the hint line of a zoomed pane does not name the way out: %q", h)
	}
	g.press("?")
	if words := readWords(g.view()); !strings.Contains(words, "z unzoom") {
		t.Errorf("? does not list z as unzoom:\n%s", words)
	}
	g.press("?", "z")
	g.press("?")
	if words := readWords(g.view()); !strings.Contains(words, "z zoom Activity") {
		t.Errorf("? does not list z as zoom Activity:\n%s", words)
	}
}
