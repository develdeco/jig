package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestToursCutNothingWhileWidthAllows: the 60 by 20, 80 by 24 and 230 by 50
// tours over main, the chart, an issue's plan and a task's screens, with the tail
// open, long titles, long ids and a pull request into a long branch: no row is
// wider than the terminal, every box is closed or marked, a tail that cannot fit
// says so, and where the width allows no id, title or pull request is cut.
func TestToursCutNothingWhileWidthAllows(t *testing.T) {
	t.Parallel()
	ids := [3]string{"round-totals", "fix-4-cart-test", "cart-total"}
	for _, size := range [][2]int{{60, 21}, {80, 24}, {230, 50}} {
		w, h := size[0], size[1]
		// check is what every screen of the tour owes the person.
		check := func(t *testing.T, g *rig, what string) string {
			t.Helper()
			g.send(tea.WindowSizeMsg{Width: w, Height: h})
			view := stripANSI(g.view())
			wantNoRowWider(t, strings.Split(view, "\n"), w)
			wantBoxesClosed(t, view)
			if !strings.Contains(view, "tail ─") && !strings.Contains(view, "the tail needs 8 body rows, this screen has ") {
				t.Errorf("%s with the tail open shows neither the tail nor why it cannot: \n%s", what, view)
			}
			return view
		}
		t.Run(fmt.Sprintf("main %dx%d", w, h), func(t *testing.T) {
			t.Parallel()
			g := newRig(t, "")
			rowIDs, titles, rows := longRows()
			g.m.w.setRoster(rows)
			g.m.w.gen++
			g.press("t")
			view := check(t, g, "main")
			if w >= 230 {
				for _, id := range rowIDs {
					wantRowWhole(t, strings.Split(view, "\n"), id, titles[id], w)
				}
			}
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
			pr := digestPR{Repo: "demo", Number: 512, URL: "https://example.test/acme/demo/pull/512", State: "open", Target: planBranch}
			g := planRig(t, planSpec{tasks: tasks, titles: titles, landed: map[string]bool{tasks[0]: true},
				issue: &digestInfo{Kind: "issue", State: "published", PRs: []digestPR{pr}}})
			g.press("t")
			view := check(t, g, "the plan")
			if w >= 230 {
				for _, id := range tasks {
					wantRowWhole(t, strings.Split(view, "\n"), id, titles[id], w)
				}
				if !strings.Contains(view, "PR #512 into "+planBranch) {
					t.Errorf("the pull request is not named whole:\n%s", view)
				}
			}
		})
		for name, screen := range map[string]screenKind{"ticket": screenTicket, "brief": screenBrief, "build": screenBuild, "review": screenReview, "publish": screenPublish, "land": screenLand} {
			t.Run(fmt.Sprintf("%s %dx%d", name, w, h), func(t *testing.T) {
				t.Parallel()
				g := longStepsRig(t, w, h, ids, true)
				if screen != screenTicket {
					g.m.nav = append(g.m.nav, navEntry{screen: screen, ticket: "SHOP-1"})
				}
				g.press("t")
				check(t, g, name)
			})
		}
	}
}
