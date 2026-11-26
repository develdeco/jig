package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// paneTour presses tab once for each pane of the screen and returns the panes in
// the order the focus visited them, ending where it began.
func paneTour(t *testing.T, g *rig) []string {
	t.Helper()
	g.view()
	n := len(g.m.panesNow)
	start := g.m.focusedPane()
	var tour []string
	for i := 0; i < n; i++ {
		g.press("tab")
		g.view()
		tour = append(tour, g.m.focusedPane())
	}
	if tour[len(tour)-1] != start {
		t.Errorf("a tour of %d tabs ends on %q, want %q where it began (%v)", n, tour[len(tour)-1], start, tour)
	}
	return tour
}

// TestChartAndPlanPanesTakeTheCursor: on the chart, the issue's plan and a
// multi-task's screen tab tours every pane, a pane of rows has its row under the
// cursor and enter opens that row's ticket, and a pane of text has none.
func TestChartAndPlanPanesTakeTheCursor(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{60, 20}, {80, 24}, {230, 50}} {
		for name, mk := range map[string]func() *rig{
			"chart": func() *rig { return chartScreenRig(t) },
			"plan":  func() *rig { return planRig(t, planSpec{}) },
			"multi": func() *rig {
				g := planRig(t, planSpec{multi: true})
				g.m.height, g.m.width = 40, 100
				onRow(t, g, "SHOP-7")
				g.press("enter")
				if g.m.Screen() != "ticket" {
					t.Fatalf("enter opened %s, want the multi-task's screen", g.m.Screen())
				}
				return g
			},
		} {
			g := mk()
			g.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			g.view()
			panes := append([]string(nil), g.m.panesNow...)
			if len(panes) < 3 {
				t.Fatalf("%s %v: the screen has the panes %v", name, size, panes)
			}
			begin := 0
			for i, p := range panes {
				if p == g.m.focusedPane() {
					begin = i
				}
			}
			tour := paneTour(t, g)
			var want []string
			for i := 1; i <= len(panes); i++ {
				want = append(want, panes[(begin+i)%len(panes)])
			}
			if fmt.Sprint(tour) != fmt.Sprint(want) {
				t.Errorf("%s %v: the tour went %v, want the panes in the order drawn, %v", name, size, tour, want)
			}
			rowPanes := 0
			for _, title := range panes {
				g.tabTo(title)
				ref := g.m.selected()
				switch title {
				case "Tickets", "Tasks", "Activity":
					rowPanes++
					if ref == nil || (ref.kind != rowTicket && ref.kind != rowCommit) {
						t.Errorf("%s %v: the %s pane's cursor is on %+v, want a ticket's row", name, size, title, ref)
						continue
					}
					if ref.kind == rowTicket {
						screen := g.m.Screen()
						depth := len(g.m.nav)
						g.press("enter")
						if len(g.m.nav) != depth+1 {
							t.Errorf("%s %v: enter in %s on %+v stays on %s", name, size, title, ref, screen)
							break
						}
						g.press("esc")
						g.view()
						if g.m.focusedPane() != title {
							t.Errorf("%s %v: back from the ticket the focus is on %q, want %q", name, size, g.m.focusedPane(), title)
						}
					}
				default:
					if ref != nil {
						t.Errorf("%s %v: the text pane %s has the row %+v under its cursor", name, size, title, ref)
					}
					depth := len(g.m.nav)
					g.press("enter")
					if len(g.m.nav) != depth {
						t.Errorf("%s %v: enter in the text pane %s opened %s", name, size, title, g.m.Screen())
						g.press("esc")
					}
				}
			}
			if rowPanes < 2 {
				t.Errorf("%s %v: only %d panes took a cursor: %v", name, size, rowPanes, panes)
			}
		}
	}
}

// TestAChartActivityLineOpensItsTicketAndACommitNothing: a line of a chart's
// activity opens the ticket it is about; a store commit among them opens nothing
// and says so; the keys act on the ticket of the line.
func TestAChartActivityLineOpensItsTicketAndACommitNothing(t *testing.T) {
	t.Parallel()
	g := historyRig(t, storeCommit("a", at(14, 25), "chart checkout: tighten the destination"))
	g.onLog(func(r rowRef) bool { return r.kind == rowTicket && r.ticket == "JIG-4" && r.container != "" })
	if got := g.m.selected(); got == nil || got.ticket != "JIG-4" {
		t.Fatalf("the cursor is on %+v, want a line of JIG-4", got)
	}
	g.press("enter")
	if g.m.Screen() != "ticket" || g.m.top().ticket != "JIG-4" {
		t.Fatalf("a line of JIG-4 opens %s %q, want JIG-4's screen", g.m.Screen(), g.m.top().ticket)
	}
	g.press("esc")
	g.view()
	if got := g.m.focusedPane(); got != "Activity" || g.m.Screen() != "chart" {
		t.Fatalf("back from the ticket: %s with the focus on %q, want the chart's Activity", g.m.Screen(), got)
	}
	commit := g.onLog(func(r rowRef) bool { return r.kind == rowCommit })
	g.press("enter")
	if g.m.Screen() != "chart" || g.m.notice != commitOpensNothing {
		t.Errorf("a store commit (%+v) opens %s with the notice %q, want nothing and %q", commit, g.m.Screen(), g.m.notice, commitOpensNothing)
	}
	// A line arriving above the cursor does not move it.
	full := g.m.page().windowed["Activity"]
	g.press("down")
	was := g.m.selected()
	g.feed(jl(at(30, 0), "JIG-4", "", "publish-done"))
	if got := g.m.selected(); got == nil || got.container != was.container {
		t.Errorf("a line that came in moved the cursor from %+v to %+v", was, got)
	}
	if now := g.m.page().windowed["Activity"]; len(now) <= len(full) {
		t.Errorf("the new line is not in the pane: %d rows after %d", len(now), len(full))
	}
}

// TestTheDetailPaneFollowsTheTicketsPaneWhateverHasTheFocus: the Detail pane
// is about the ticket under the Tickets cursor, whether the focus is in Tickets
// or in any other pane.
func TestTheDetailPaneFollowsTheTicketsPaneWhateverHasTheFocus(t *testing.T) {
	t.Parallel()
	g := chartScreenRig(t)
	g.send(tea.WindowSizeMsg{Width: 100, Height: 60})
	detailOf := func() string {
		for _, l := range strings.Split(g.view(), "\n") {
			if strings.Contains(l, "╭─ Detail") {
				return l
			}
		}
		t.Fatalf("no Detail pane:\n%s", g.view())
		return ""
	}
	first := g.m.selected()
	if first == nil || first.kind != rowTicket || !strings.Contains(detailOf(), first.id) {
		t.Fatalf("the Detail pane %q is not about %+v", detailOf(), first)
	}
	g.press("down")
	moved := g.m.selected()
	if moved == nil || moved.id == first.id || !strings.Contains(detailOf(), moved.id) {
		t.Fatalf("down in Tickets: the Detail pane %q is not about %+v", detailOf(), moved)
	}
	g.tabTo("Activity")
	g.press("down", "down")
	if d := detailOf(); !strings.Contains(d, moved.id) {
		t.Errorf("a move in Activity changed the Detail pane to %q, want it about %s", d, moved.id)
	}
	g.tabTo("Run")
	if d := detailOf(); !strings.Contains(d, moved.id) {
		t.Errorf("the focus on Run changed the Detail pane to %q, want it about %s", d, moved.id)
	}
	g.tabTo("Tickets")
	if got := g.m.selected(); got == nil || got.id != moved.id {
		t.Errorf("back in Tickets the cursor is on %+v, want %s", got, moved.id)
	}
}
