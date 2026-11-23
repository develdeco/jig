package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestChartAndPlanLaidOutAtThreeSizes: the chart screen and an issue's plan,
// with long titles, a pull request into a long branch name and a landing,
// draw every id, title and pull request whole while the width allows, and no
// row is wider than the terminal.
func TestChartAndPlanLaidOutAtThreeSizes(t *testing.T) {
	t.Parallel()
	for _, size := range tallSizes {
		w, h := size[0], size[1]
		t.Run(fmt.Sprintf("chart %dx%d", w, h), func(t *testing.T) {
			t.Parallel()
			g := newRig(t, "")
			stream, status := chartTickets()
			titles := map[string]string{}
			retitle := func(v map[string]any) {
				for i, e := range v["tickets"].([]map[string]any) {
					if id, _ := e["id"].(string); id != "" {
						titles[id] = longTitle(i + 1)
						e["title"] = titles[id]
					}
				}
			}
			answerChart(g, chartRoster(checkoutListing()), "checkout", checkoutJSON(retitle), status)
			g.feed(stream...)
			g.send(tea.WindowSizeMsg{Width: w, Height: h})
			g.press("end", "enter")
			lines := strings.Split(stripANSI(g.view()), "\n")
			wantNoRowWider(t, lines, w)
			for id, title := range titles {
				wantRowWhole(t, lines, id, title, w)
			}
		})
		t.Run(fmt.Sprintf("plan %dx%d", w, h), func(t *testing.T) {
			t.Parallel()
			titles := map[string]string{}
			var ids []string
			for i := 1; i <= 3; i++ {
				id := fmt.Sprintf("SHOP-%020d", i)
				ids = append(ids, id)
				titles[id] = longTitle(i)
			}
			pr := digestPR{Repo: "demo", Number: 512, URL: "https://example.test/acme/demo/pull/512", State: "open", Target: planBranch}
			g := planRig(t, planSpec{
				tasks: ids, titles: titles,
				landed: map[string]bool{ids[0]: true},
				issue:  &digestInfo{Kind: "issue", State: "published", PRs: []digestPR{pr}},
			})
			g.send(tea.WindowSizeMsg{Width: w, Height: h})
			view := stripANSI(g.view())
			lines := strings.Split(view, "\n")
			wantNoRowWider(t, lines, w)
			for _, id := range ids {
				wantRowWhole(t, lines, id, titles[id], w)
			}
			if w >= 230 && !strings.Contains(view, "PR #512 into "+planBranch) {
				t.Errorf("the pull request is not named whole at %d wide:\n%s", w, view)
			}
		})
	}
}
