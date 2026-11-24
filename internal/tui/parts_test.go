package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/events"
)

// listOf is a list panel of n rows under three columns: an icon, a name and
// some words.
func listOf(m *Model, n int) page {
	rows := make([]listRow, n)
	for i := range rows {
		rows[i] = listRow{ref: &rowRef{kind: rowSlice, id: fmt.Sprint(i)}, cells: []string{"o", fmt.Sprintf("row-%d", i), "words of the row"}}
	}
	var p page
	m.listPanel(&p, listSpec{title: "Things", summary: fmt.Sprint(n), cols: []column{{}, {gap: 1}, {free: true, gap: 2}}, names: []string{"", "thing", "words"}, rows: rows})
	return p
}

// TestAListPanelNamesItsColumns: a list panel is its top edge with a title and
// a summary, a row of the columns' names laid out with the rows so each name is
// over its cells, the rows, and its bottom edge; a list with no rows has no
// names.
func TestAListPanelNamesItsColumns(t *testing.T) {
	t.Parallel()
	m := newRig(t, "JIG-1").m
	p := listOf(m, 3)
	if got := len(p.lines); got != 6 {
		t.Fatalf("the panel has %d lines, want its two edges, the names and three rows", got)
	}
	if top := p.lines[0]; top.box != boxTop || top.text != "Things" || top.right != "3" {
		t.Errorf("the top edge is %+v, want the title and the summary", top)
	}
	names, first := p.lines[1], p.lines[2]
	if !names.names || names.box != boxMid || names.ref != nil {
		t.Errorf("the line under the top edge is %+v, want the names row", names)
	}
	if strings.Index(names.text, "thing") != strings.Index(first.text, "row-0") || strings.Index(names.text, "words") != strings.Index(first.text, "words of") {
		t.Errorf("the names are not over their cells:\n%s\n%s", names.text, first.text)
	}
	if p.lines[5].box != boxBottom {
		t.Errorf("the panel does not end with its bottom edge")
	}

	var none page
	m.listPanel(&none, listSpec{title: "Things", cols: []column{{}, {free: true}}, names: []string{"a", "b"}, empty: "nothing yet"})
	for _, l := range none.lines {
		if l.names {
			t.Errorf("an empty list has a names row: %+v", l)
		}
	}
	if len(none.lines) != 3 || !strings.Contains(none.lines[1].text, "nothing yet") {
		t.Errorf("an empty list is %+v, want its edges around its words", none.lines)
	}
}

// TestANamesRowIsNeverUnderTheCursorAndStaysWhenTheWindowCutsTheList: the
// cursor's rows are the list's rows and never the names; a window that starts
// inside the list or ends inside it draws the names directly under the top edge.
func TestANamesRowIsNeverUnderTheCursorAndStaysWhenTheWindowCutsTheList(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	m := g.m
	p := listOf(m, 30)
	if refs := p.refs(); len(refs) != 30 {
		t.Fatalf("the cursor has %d rows, want the list's 30 and not the names", len(refs))
	}
	for _, sel := range []int{0, 1, 7, 15, 28, 29} {
		start := m.windowStart(p, sel, 10, false)
		rows, from, to := m.windowRows(p, sel, start, 10, 60, "")
		if len(rows) > 10 {
			t.Errorf("cursor %d: %d rows in a window of 10", sel, len(rows))
		}
		text := strings.Join(rows, "\n")
		if !strings.Contains(text, fmt.Sprintf("▸ o row-%d", sel)) {
			t.Errorf("cursor %d: its row is not in the window:\n%s", sel, text)
		}
		if from == 0 {
			continue
		}
		if !strings.Contains(rows[0], "╭─ Things") || !strings.Contains(rows[1], "thing") || !strings.Contains(rows[1], "words") {
			t.Errorf("cursor %d: a window from line %d to %d lacks the names under the top edge:\n%s", sel, from, to, text)
		}
		for _, row := range rows[2:] {
			if strings.Contains(row, "│  thing") {
				t.Errorf("cursor %d: the names are drawn twice:\n%s", sel, text)
			}
		}
	}
}

// TestTicketScreenSectionsArePanels: every section of the ticket screen is a
// panel with a capitalized title, every list in one starts with its names, no
// section is a bare lowercase heading, and every row the cursor can stand on is
// inside a panel; at three sizes.
func TestTicketScreenSectionsArePanels(t *testing.T) {
	t.Parallel()
	names := map[string]string{"Steps": "step", "Activity": "time", "Build": "sub-step", "Brief": "sub-step", "Review": "sub-step"}
	for name, mk := range map[string]func() *rig{
		"a build with a question": func() *rig { g := newRig(t, "JIG-1"); g.feed(jig1()...); return g },
		"a build with the card":   func() *rig { return shop1Rig(t) },
	} {
		for _, size := range [][2]int{{60, 20}, {80, 24}, {230, 50}} {
			g := mk()
			g.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			lines := strings.Split(g.view(), "\n")
			title, inside := "", 0
			for i, l := range lines {
				switch {
				case strings.HasPrefix(l, "  ╭─ "):
					title, _, _ = strings.Cut(strings.TrimPrefix(l, "  ╭─ "), " ─")
					inside = 0
					if title != "" && strings.ToUpper(title[:1]) != title[:1] {
						t.Errorf("%s %v: the panel %q has no capital", name, size, title)
					}
				case strings.HasPrefix(l, "  ╰"):
					title = ""
				case strings.HasPrefix(l, "  │"):
					inside++
					if want, ok := names[title]; ok && inside == 1 && !strings.Contains(l, want) && !strings.Contains(l, "took") {
						t.Errorf("%s %v: the first line of %s is %q, want the names with %q", name, size, title, l, want)
					}
				case strings.HasPrefix(l, "▸"):
					t.Errorf("%s %v: the cursor's row is outside a panel at line %d: %q", name, size, i, l)
				}
				for _, bare := range []string{"needs you", "asks", "sessions", "slices"} {
					if strings.TrimSpace(l) == bare {
						t.Errorf("%s %v: %q is a bare heading", name, size, bare)
					}
				}
			}
		}
	}
}

// unframed is a screen with its panels' frames taken off: a panel's top edge is
// its title alone, a row its text under the cursor's mark, and its bottom edge
// goes. A test that reads the words of a section reads them through it.
func unframed(view string) string {
	var out []string
	for _, l := range strings.Split(view, "\n") {
		switch {
		case strings.HasPrefix(l, "  ╭─ "):
			title, _, _ := strings.Cut(strings.TrimPrefix(l, "  ╭─ "), " ─")
			out = append(out, "  "+title)
		case strings.HasPrefix(l, "  ╰"):
		case strings.HasPrefix(l, "  │"):
			out = append(out, strings.TrimRight(strings.TrimSuffix(strings.TrimPrefix(l, "  │"), "│"), " "))
		default:
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// activityLine is an activity row's cells read as one line, as a person reads it
// on a wide screen.
func (m *Model) activityLine(t *ticket, ls subLabels, l events.Envelope) string {
	c := m.activityCells(t, ls, l)
	return c[0] + " " + c[1] + " · " + c[2]
}
