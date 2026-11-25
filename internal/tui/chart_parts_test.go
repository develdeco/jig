package tui

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// panelBody is the lines inside the panel titled title on a screen, from the
// line under its top edge to the one above its bottom edge, frames kept.
func panelBody(lines []string, title string) []string {
	for i, l := range lines {
		if !strings.HasPrefix(l, "  ╭─ "+title+" ") {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "  ╰") {
				return lines[i+1 : j]
			}
		}
	}
	return nil
}

var ageAtTheEnd = regexp.MustCompile(` \d+[smhd] *│$`)

// TestChartAndPlanListsNameTheirColumnsAndAges: the chart's Tickets and
// Activity, an issue's Tasks and a multi-task's Tasks are lists under the names
// of their columns, a ticket's row ends with its age, and the row under the
// cursor is lit across its panel in a colored terminal; at three sizes.
func TestChartAndPlanListsNameTheirColumnsAndAges(t *testing.T) {
	t.Parallel()
	for _, size := range layoutSizes {
		w, h := size[0], size[1]
		for name, c := range map[string]struct {
			rig    func() *rig
			panels map[string]string // title -> a word of its names row
			aged   bool
		}{
			"the chart": {func() *rig { return chartScreenRig(t) }, map[string]string{"Tickets": "ticket", "Activity": "time"}, true},
			"an issue's plan": {func() *rig {
				return planRig(t, planSpec{tasks: []string{"SHOP-1", "SHOP-2"}, titles: map[string]string{"SHOP-1": longTitle(1), "SHOP-2": longTitle(2)}})
			}, map[string]string{"Tasks": "ticket"}, false},
			"a multi-task": {func() *rig {
				g := planRig(t, planSpec{multi: true})
				onRow(t, g, "SHOP-7")
				g.press("enter")
				return g
			}, map[string]string{"Tasks": "ticket"}, false},
		} {
			g := c.rig()
			g.send(tea.WindowSizeMsg{Width: w, Height: max(h, 40)})
			lines := strings.Split(stripANSI(g.view()), "\n")
			for title, word := range c.panels {
				body := panelBody(lines, title)
				if len(body) == 0 || !strings.Contains(body[0], word) {
					t.Errorf("%s %dx%d: the %s panel does not start with its names (%q):\n%s", name, w, h, title, word, strings.Join(lines, "\n"))
				}
			}
			if c.aged {
				ended := false
				for _, l := range panelBody(lines, "Tickets") {
					ended = ended || ageAtTheEnd.MatchString(l)
				}
				if !ended {
					t.Errorf("%s %dx%d: no ticket row ends with its age:\n%s", name, w, h, strings.Join(lines, "\n"))
				}
			}

			// The row under the cursor is lit from border to border.
			g.m.st = styles{r: colored()}
			sel := ""
			for _, l := range strings.Split(g.view(), "\n") {
				if strings.Contains(stripANSI(l), "│▸") {
					sel = l
				}
			}
			if sel == "" {
				t.Fatalf("%s %dx%d: no row under the cursor", name, w, h)
			}
			cells, lit := litCells(sel)
			first, last := -1, -1
			for i, cell := range cells {
				if cell == "│" {
					if first < 0 {
						first = i
					}
					last = i
				}
			}
			for i := first + 1; i < last; i++ {
				if !lit[i] {
					t.Errorf("%s %dx%d: cell %d of the row under the cursor is not lit: %q", name, w, h, i, sel)
					break
				}
			}
		}
	}
}
