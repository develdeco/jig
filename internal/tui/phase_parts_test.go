package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// bareHeadings are the words the sections of the phase screens were headed by
// when they were not panels.
var bareHeadings = []string{"slices", "sessions", "sections", "rounds", "findings", "probes", "sent back to the build", "asks", "verification", "questions", "timeline", "needs you"}

// wantPanels checks a screen drawn as lines: no section is a bare heading, every
// panel has a capitalized title, the first line inside each panel in names is the
// names of its columns, and no row the cursor stands on is outside a panel.
func wantPanels(t *testing.T, where string, view string, names map[string]string) {
	t.Helper()
	lines := strings.Split(stripANSI(view), "\n")
	title, inside := "", 0
	seen := map[string]bool{}
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "  ╭─ "):
			title, _, _ = strings.Cut(strings.TrimPrefix(l, "  ╭─ "), " ─")
			inside = 0
			seen[title] = true
			if title != "" && strings.ToUpper(title[:1]) != title[:1] {
				t.Errorf("%s: the panel %q has no capital", where, title)
			}
		case strings.HasPrefix(l, "  ╰"):
			title = ""
		case strings.HasPrefix(l, "  │"):
			inside++
			if want, ok := names[title]; ok && inside == 1 && !strings.Contains(l, want) && !strings.Contains(l, "↑") && !strings.Contains(l, "nothing yet") && !strings.Contains(l, "…") {
				t.Errorf("%s: the first line of %s is %q, want the names with %q", where, title, l, want)
			}
		case strings.HasPrefix(l, "▸"):
			t.Errorf("%s: the cursor's row is outside a panel at line %d: %q", where, i, l)
		}
		for _, bare := range bareHeadings {
			// A bare heading is a line of its own after a blank one, not the end of
			// a sentence that wrapped.
			if (l == "  "+bare || strings.HasPrefix(l, "  round 1 findings")) && i > 0 && lines[i-1] == "" {
				t.Errorf("%s: %q is a bare heading at line %d", where, strings.TrimSpace(l), i)
			}
		}
	}
	for title := range names {
		if !seen[title] {
			t.Errorf("%s: the screen has no %s panel:\n%s", where, title, view)
		}
	}
}

// TestBuildBriefAndSliceSectionsArePanels: the build, brief and slice screens
// draw each section as a panel with a capitalized title, every list under the
// names of its columns, and no row the cursor stands on outside a panel; at
// three sizes.
func TestBuildBriefAndSliceSectionsArePanels(t *testing.T) {
	t.Parallel()
	for _, size := range layoutSizes {
		w, h := size[0], size[1]
		for name, c := range map[string]struct {
			keys  []string
			names map[string]string
		}{
			"the build": {[]string{"enter"}, map[string]string{"Slices": "slice", "Sessions": "harness", "Build activity": "time"}},
			"the brief": {[]string{"enter", "esc", "1"}, map[string]string{"Parts": "kind", "Brief activity": "time"}},
			"a slice":   {[]string{"enter", "down", "down", "enter"}, map[string]string{"Timeline": "time"}},
		} {
			for _, rows := range []int{h, max(h, 70)} {
				g := newRig(t, "JIG-1")
				g.r.answer("status JIG-1 --store /s", statusJIG1Brief(), 0)
				g.feed(jig1Records()...)
				g.send(tea.WindowSizeMsg{Width: w, Height: rows})
				g.press(c.keys...)
				want := c.names
				if rows == h {
					// A short screen need not hold every panel, but the ones it
					// shows are as panels are.
					want = nil
				}
				wantPanels(t, fmt.Sprintf("%s %dx%d", name, w, rows), g.view(), want)
			}
		}
	}
}
