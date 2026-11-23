package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestHeaderTitle: every screen of a ticket names it by id and title in the
// header, a ticket opened by the launch argument has only its id until a
// roster read lists it, and a narrow width cuts the title before the step.
func TestHeaderTitle(t *testing.T) {
	screens := []struct {
		name  string
		entry navEntry
		step  string
	}{
		{"ticket", navEntry{screen: screenTicket, ticket: "JIG-1"}, ""},
		{"brief", navEntry{screen: screenBrief, ticket: "JIG-1"}, "1 brief"},
		{"build", navEntry{screen: screenBuild, ticket: "JIG-1"}, "2 build"},
		{"review", navEntry{screen: screenReview, ticket: "JIG-1"}, "3 review"},
		{"publish", navEntry{screen: screenPublish, ticket: "JIG-1"}, "4 publish"},
		{"slice", navEntry{screen: screenSlice, ticket: "JIG-1", slice: "a"}, "slice"},
	}
	head := func(g *rig) string { return strings.SplitN(g.view(), "\n", 2)[0] }
	for _, sc := range screens {
		for _, w := range []int{80, 52} {
			t.Run(sc.name, func(t *testing.T) {
				g := newRig(t, "JIG-1")
				g.m.nav = []navEntry{{screen: screenMain}, sc.entry}
				g.send(tea.WindowSizeMsg{Width: w, Height: 24})
				if h := head(g); !strings.Contains(h, "JIG-1") || strings.Contains(h, "Greeting copy") {
					t.Errorf("before the roster read, width %d: %q", w, h)
				}
				g.feed()
				h := head(g)
				if !strings.Contains(h, "JIG-1") {
					t.Errorf("after the roster read, width %d: %q lost the id", w, h)
				}
				if w == 80 && !strings.Contains(h, "Greeting copy") {
					t.Errorf("after the roster read, width %d: %q has no title", w, h)
				}
				if w == 52 && sc.step != "" && !strings.Contains(h, sc.step) {
					t.Errorf("width %d: %q cut the step before the title", w, h)
				}
			})
		}
	}
}
