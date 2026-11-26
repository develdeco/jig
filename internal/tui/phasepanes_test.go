package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// phaseRig is JIG-2 as it stands once published, drawn at w by h, on the ticket
// screen; screen opens one of its steps' screens by the key that names it.
func phaseRig(t *testing.T, w, h int) *rig {
	t.Helper()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", statusJIG2Done(), 0)
	g.send(tea.WindowSizeMsg{Width: w, Height: h})
	g.feed(jig2()...)
	g.view()
	return g
}

// TestEveryPhaseScreenIsATourOfItsPanes: on each screen of a ticket's steps and on
// the slice's, tab visits every panel and comes back to where it began, a pane
// of rows has a row under its cursor and keeps it while the focus is elsewhere,
// a pane of text has none, and each pane zooms and comes back.
func TestEveryPhaseScreenIsATourOfItsPanes(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{60, 20}, {80, 24}, {230, 50}} {
		for _, key := range []string{"1", "2", "3", "4", "5", "slice"} {
			var g *rig
			switch key {
			case "1", "slice":
				// JIG-1 has a brief to show and a slice that asked a question.
				g = newRig(t, "JIG-1")
				g.r.answer("status JIG-1 --store /s", statusJIG1Brief(), 0)
				g.r.answer("brief show JIG-1 --text --store /s", briefTextJIG1(), 0)
				g.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				g.feed(jig1Records()...)
				g.view()
			default:
				g = phaseRig(t, size[0], size[1])
			}
			if key == "slice" {
				g.press("2")
				g.tabTo("Slices").press("down", "down", "enter")
				if g.m.Screen() != "slice" {
					t.Fatalf("%v: enter on a slice opens %s", size, g.m.Screen())
				}
			} else {
				g.press(key)
			}
			name := g.m.Screen()
			g.view()
			panes := append([]string(nil), g.m.panesNow...)
			if len(panes) < 2 {
				t.Errorf("%s %v: the screen has the panes %v, want more than one", name, size, panes)
				continue
			}
			tour := paneTour(t, g)
			if len(tour) != len(panes) {
				t.Errorf("%s %v: a tour of %v visited %v", name, size, panes, tour)
			}
			rowsSeen := 0
			for _, title := range panes {
				g.tabTo(title)
				ref := g.m.selected()
				g.press("z")
				zoomed := g.view()
				if n := strings.Count(zoomed, "╭─"); n != 1 || !strings.Contains(zoomed, "╭─ "+title) {
					t.Errorf("%s %v: zoomed on %s the frame has %d panels:\n%s", name, size, title, n, zoomed)
				}
				if got := g.m.selected(); (got == nil) != (ref == nil) || got != nil && *got != *ref {
					t.Errorf("%s %v: zooming %s moved its cursor from %+v to %+v", name, size, title, ref, got)
				}
				g.press("z")
				if ref != nil {
					rowsSeen++
				}
			}
			// A pane keeps its cursor while the focus is elsewhere.
			for _, title := range panes {
				g.tabTo(title)
				ref := g.m.selected()
				if ref == nil {
					continue
				}
				g.press("down")
				moved := g.m.selected()
				g.tabTo(panes[(indexOf(panes, title)+1)%len(panes)])
				g.tabTo(title)
				if got := g.m.selected(); got == nil || moved == nil || *got != *moved {
					t.Errorf("%s %v: back in %s the cursor is on %+v, want %+v", name, size, title, got, moved)
				}
			}
			if rowsSeen == 0 && name != "land" {
				t.Errorf("%s %v: no pane took a cursor: %v", name, size, panes)
			}
		}
	}
}

func indexOf(ss []string, s string) int {
	for i, x := range ss {
		if x == s {
			return i
		}
	}
	return -1
}

// TestAStepActivityPaneHoldsEveryLineAndOpensWhatItIsAbout: a step's activity is a
// pane with a row for each line of the step, the window follows its cursor, and
// enter opens what a line is about: a slice, its question while it is open.
func TestAStepActivityPaneHoldsEveryLineAndOpensWhatItIsAbout(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{60, 20}, {80, 24}, {230, 50}} {
		g := newQuietRig(t, "JIG-1")
		g.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		g.feed(jig1()...)
		g.press("enter")
		if g.m.Screen() != "build" {
			t.Fatalf("%v: on %s, want build", size, g.m.Screen())
		}
		g.tabTo("Build activity")
		full := g.m.page().windowed["Build activity"]
		if want := len(g.m.w.tickets["JIG-1"].stepLines(stepBuild)); len(full) != want || want < 8 {
			t.Fatalf("%v: the pane holds %d rows for the step's %d lines", size, len(full), want)
		}
		first := g.m.selected()
		if first == nil || first.kind != rowLog || first.id != full[0].id {
			t.Fatalf("%v: the cursor begins on %+v, want the newest line", size, first)
		}
		g.press("end")
		if got := g.m.selected(); got == nil || got.id != full[len(full)-1].id {
			t.Fatalf("%v: end reaches %+v, want the oldest line", size, got)
		}

		g.onLine("Build activity", func(r rowRef) bool { return r.slice == "a" && r.question == "" })
		g.press("enter")
		if g.m.Screen() != "slice" || g.m.top().slice != "a" {
			t.Errorf("%v: a line of slice a opens %s %q", size, g.m.Screen(), g.m.top().slice)
		}
		g.press("esc")
		g.view()
		if g.m.Screen() != "build" || g.m.focusedPane() != "Build activity" {
			t.Errorf("%v: back from the slice: %s with the focus on %q", size, g.m.Screen(), g.m.focusedPane())
		}
		if got := g.m.selected(); got == nil || got.slice != "a" {
			t.Errorf("%v: back on the build the cursor is on %+v, want the line of a", size, got)
		}

		g.onLine("Build activity", func(r rowRef) bool { return r.question == "q-001" })
		g.press("enter")
		if g.m.input == nil || g.m.input.qid != "q-001" {
			t.Errorf("%v: a line of the open question opens %+v, want its answer prompt", size, g.m.input)
		}
		g.press("esc")
	}
}
