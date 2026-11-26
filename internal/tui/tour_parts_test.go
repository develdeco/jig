package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// listNames are the panels that are lists, each with a word its names row says:
// the other panels (a need, a run, a detail, a map, the issue's stages, what
// needs a person) are sentences and have no names.
var listNames = map[string]string{
	"Steps": "step", "Tickets": "ticket", "Tasks": "ticket", "Activity": "time",
	"Brief activity": "time", "Build activity": "time", "Review activity": "time",
	"Publish activity": "time", "Land activity": "time", "Slices": "slice",
	"Sessions": "harness", "Sections": "section", "Rounds": "round", "Findings": "risk",
	"Probes": "round", "Sent back": "slice", "Asks": "risk",
	"Checklist": "step", "Pull requests": "repo", "Verification": "commit",
	"Questions": "question", "Timeline": "time",
	"Needs you": "ticket", "Running": "ticket", "Ready": "ticket", "Waiting": "ticket",
	"Charts": "name", "No chart": "ticket",
	// The sub-steps of the step under the cursor.
	"Brief": "sub-step", "Build": "sub-step", "Review": "sub-step", "Publish": "sub-step", "Land": "sub-step",
}

// wantEveryRowInAPanel checks one drawn screen: no row the cursor can stand on
// is outside a panel, every panel's title is capitalized and its edge closed at
// the screen's width, every list panel of more than one line starts with its
// names row, and under the colored renderer the row under the cursor is lit
// from border to border.
func wantEveryRowInAPanel(t *testing.T, where string, g *rig, w int, sentences ...string) {
	t.Helper()
	plain := g.view()
	lines := strings.Split(stripANSI(plain), "\n")
	title, inside, body := "", 0, 0
	for i, l := range lines {
		if width(l) > w {
			t.Errorf("%s: line %d is %d wide in %d: %q", where, i, width(l), w, l)
		}
		switch {
		case strings.HasPrefix(l, "  ╭─ "):
			title, _, _ = strings.Cut(strings.TrimPrefix(l, "  ╭─ "), " ─")
			inside = 0
			body = 0
			for _, next := range lines[i+1:] {
				if strings.HasPrefix(next, "  ╰") {
					break
				}
				body++
			}
			if title == "" || strings.ToUpper(title[:1]) != title[:1] {
				t.Errorf("%s: the panel %q has no capital", where, title)
			}
			if !strings.HasSuffix(l, "─╮") {
				t.Errorf("%s: the top edge of %s is not closed: %q", where, title, l)
			}
		case strings.HasPrefix(l, "  ╰"):
			title = ""
		case strings.HasPrefix(l, "  │"):
			inside++
			word, isList := listNames[title]
			for _, s := range sentences {
				// A panel of what needs a person is sentences, not a list.
				isList = isList && title != s
			}
			if inside == 1 && isList && body > 1 && !strings.Contains(l, word) {
				t.Errorf("%s: the first line of %s is %q, want the names with %q", where, title, l, word)
			}
		case strings.Contains(l, "▸"):
			t.Errorf("%s: the cursor's row is outside a panel at line %d: %q", where, i, l)
		}
	}

	// The row under the cursor is lit across its panel.
	was := g.m.st
	g.m.st = styles{r: colored()}
	defer func() { g.m.st = was }()
	for _, l := range strings.Split(g.view(), "\n") {
		if !strings.Contains(stripANSI(l), "│▸") {
			continue
		}
		cells, lit := litCells(l)
		first, last := -1, -1
		var borders []int
		for i, cell := range cells {
			if cell == "│" {
				if first < 0 {
					first = i
				}
				last = i
				borders = append(borders, i)
			}
		}
		if len(borders) == 4 {
			// A list with its detail beside it: the row is lit across the list's
			// own panel, not the detail's.
			last = borders[1]
		}
		for i := first + 1; i < last; i++ {
			if !lit[i] {
				t.Errorf("%s: cell %d of the row under the cursor is not lit: %q", where, i, l)
				break
			}
		}
	}
}

// TestEveryRowTheCursorStandsOnIsInAPanelWithItsNames: the tours of main, a
// chart, an issue's plan, a multi-task and a task's screens, with long titles,
// at three sizes: every row the cursor can stand on is inside a panel, every
// list starts with its names, every panel is titled with a capital, and the
// selected row is lit across its panel.
func TestEveryRowTheCursorStandsOnIsInAPanelWithItsNames(t *testing.T) {
	t.Parallel()
	task := func(keys ...string) func() *rig {
		return func() *rig {
			g := newRig(t, "JIG-1")
			g.r.answer("status JIG-1 --store /s", statusJIG1Brief(), 0)
			g.feed(jig1Records()...)
			g.press(keys...)
			return g
		}
	}
	tours := []struct {
		name      string
		sentences []string
		rig       func() *rig
	}{
		{"main", nil, func() *rig { return chartRig(t) }},
		{"main with long titles", nil, func() *rig {
			g := newRig(t, "")
			_, _, rows := longRows()
			g.m.w.setRoster(rows)
			g.m.w.gen++
			return g
		}},
		{"the chart", nil, func() *rig { return chartScreenRig(t) }},
		{"an issue's plan", nil, func() *rig {
			return planRig(t, planSpec{tasks: []string{"SHOP-1", "SHOP-2"}, titles: map[string]string{"SHOP-1": longTitle(1), "SHOP-2": longTitle(2)}})
		}},
		{"a multi-task", nil, func() *rig {
			g := planRig(t, planSpec{multi: true})
			onRow(t, g, "SHOP-7")
			g.press("enter")
			return g
		}},
		{"a ticket", []string{"Needs you", "Asks"}, task()},
		{"its brief", nil, task("1")},
		{"its build", nil, task("2")},
		{"a slice", nil, task("2", "down", "down", "enter")},
		{"its review", nil, task("3")},
		{"its publish", nil, task("4")},
		{"its land", nil, task("5")},
	}
	for _, tour := range tours {
		for _, size := range layoutSizes {
			g := tour.rig()
			g.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			wantEveryRowInAPanel(t, fmt.Sprintf("%s %dx%d", tour.name, size[0], size[1]), g, size[0], tour.sentences...)
		}
	}
}
