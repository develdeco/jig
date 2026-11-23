package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/journal"
)

// gateHelp is the first help line a gate that opened a round prints: who is to
// review. Longer than a line of the screen.
const gateHelp = "Have a fresh-context reviewer (a subagent or another session) run the `review` skill: it reads the round with `jig review context JIG-2` and ends with `jig review done JIG-2`"

// panelBelowCommand are the rows of the frame from the command's line to the rule
// above the keys.
func panelBelowCommand(view string) []string {
	var out []string
	in := false
	for _, l := range strings.Split(view, "\n") {
		switch {
		case strings.HasPrefix(l, "  ✓ jig"):
			in = true
		case strings.HasPrefix(l, "───") && in:
			return out
		}
		if in {
			out = append(out, l)
		}
	}
	return out
}

// TestGateShowsTheReviewerStepWhole: after g the verb's own first help line (who
// reviews) is on the frame whole, wrapped, beneath the command, at 80 and at 100
// columns, and it goes with the line when the ticket moves.
func TestGateShowsTheReviewerStepWhole(t *testing.T) {
	t.Parallel()
	for _, width := range []int{80, 100} {
		g := newQuietRig(t, "JIG-2")
		g.r.answer("gate JIG-2 --store /s", "gate:\n  ticket: JIG-2\n  round: 1\nhelp[2]:\n  "+gateHelp+"\n  Another line\n", 0)
		g.send(tea.WindowSizeMsg{Width: width, Height: 30})
		g.feed(jig2Mid()...).feed(decided()...).press("enter", "g")
		rows := panelBelowCommand(g.view())
		if len(rows) < 3 || rows[0] != "  ✓ jig gate JIG-2" {
			t.Fatalf("width %d: the command is not alone on its line, then the help:\n%s", width, g.view())
		}
		var said []string
		for _, r := range rows[1:] {
			if !strings.HasPrefix(r, "    ") || len([]rune(r)) > width {
				t.Errorf("width %d: help row %q is not indented or is wider than the screen", width, r)
			}
			said = append(said, strings.TrimSpace(r))
		}
		if got := strings.Join(said, " "); got != gateHelp {
			t.Errorf("width %d: the help reads\n%s\nwant the verb's line whole\n%s", width, got, gateHelp)
		}
		if strings.Contains(g.view(), "Another line") {
			t.Errorf("width %d: a second help line is on the frame", width)
		}

		// The ticket moves: the line and its help go together.
		g.feed(jl(at(12, 0), "JIG-2", "", journal.EventGateOpen, func(l *journal.Line) { l.Attempt = 2 }))
		if v := g.view(); strings.Contains(v, "jig gate JIG-2") || strings.Contains(v, "fresh-context") {
			t.Errorf("width %d: the line outlived the ticket's move:\n%s", width, v)
		}
	}
}

// TestAResultLineKeepsOneLineForOtherKeys: a verb that leaves no move to someone
// else is one line, its help cut, and a refused gate is its error on one line.
func TestAResultLineKeepsOneLineForOtherKeys(t *testing.T) {
	t.Parallel()
	g := newQuietRig(t, "JIG-2")
	g.r.answer("triage JIG-2 r1-f2 dismiss --store /s", "help[1]:\n  "+gateHelp+"\n", 0)
	g.feed(jig2Mid()...).press("enter", "down", "d")
	rows := panelBelowCommand(g.view())
	if len(rows) != 1 || !strings.HasSuffix(rows[0], "…") {
		t.Errorf("a triage's result is %q, want one cut line", rows)
	}

	g = newQuietRig(t, "JIG-2")
	g.r.answer("gate JIG-2 --store /s", "error: round 1 is not recorded\ncode: GATE_NOT_GREEN\nhelp[1]:\n  "+gateHelp+"\n", 1)
	g.feed(jig2Mid()...).feed(decided()...).press("enter", "g")
	if got := lastPanel(g.view()); len(got) != 1 || strings.Contains(g.view(), "fresh-context") {
		t.Errorf("a refused gate is %q, want its one line and none of its help:\n%s", got, g.view())
	}
}
