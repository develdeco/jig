package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// lastPanel is the lines of the frame between the screen's last box and its
// keys: what the panel printed for the last command.
func lastPanel(view string) []string {
	var out []string
	for _, l := range strings.Split(view, "\n") {
		if strings.HasPrefix(l, "  ✓ jig") || strings.HasPrefix(l, "  ✖ jig") {
			out = append(out, l)
		}
	}
	return out
}

// TestLastCommandPanelIsOneLine: after a key's command the panel is one line,
// the command as typed and the verb's own first sentence when it worked, or its
// error and code when it did not, cut with an ellipsis and never the raw output.
func TestLastCommandPanelIsOneLine(t *testing.T) {
	t.Parallel()

	t.Run("a success says the verb's sentence", func(t *testing.T) {
		g := newRig(t, "JIG-2")
		g.r.answer("triage JIG-2 r1-f2 dismiss --store /s", "triage:\n  ticket: JIG-2\n  decision: dismiss\nhelp[1]:\n  The next round applies it\n", 0)
		g.feed(jig2Mid()...).press("enter", "down", "d")
		got := lastPanel(g.view())
		if len(got) != 1 || got[0] != "  ✓ jig triage JIG-2 r1-f2 dismiss  The next round applies it" {
			t.Errorf("panel = %q", got)
		}
		if v := g.view(); strings.Contains(v, "provenance") || strings.Contains(v, "ticket: JIG-2") {
			t.Errorf("the raw output is on screen:\n%s", v)
		}
	})

	t.Run("a failure says its error and its code", func(t *testing.T) {
		g := newRig(t, "JIG-2")
		g.r.answer("gate JIG-2 --store /s", "error: round 1 is not recorded\ncode: GATE_NOT_GREEN\nhelp[1]:\n  Run `jig next JIG-2`\n", 1)
		g.feed(jig2Mid()...).press("enter", "g")
		got := lastPanel(g.view())
		if len(got) != 1 || got[0] != "  ✖ jig gate JIG-2  round 1 is not recorded (GATE_NOT_GREEN)" {
			t.Errorf("panel = %q", got)
		}
	})

	t.Run("a chain whose second step failed shows the failure", func(t *testing.T) {
		g := newRig(t, "JIG-2")
		g.r.answer("publish JIG-2 --yes --store /s", "error: the head moved\ncode: PUBLISH_STALE\n", 1)
		g.feed(jig2Mid()...).press("p", "y")
		got := lastPanel(g.view())
		if len(got) != 1 || got[0] != "  ✖ jig publish JIG-2 --yes  the head moved (PUBLISH_STALE)" {
			t.Errorf("panel = %q, want only the failed step", got)
		}
	})

	t.Run("a long sentence is cut", func(t *testing.T) {
		g := newRig(t, "JIG-2")
		g.r.answer("triage JIG-2 r1-f2 dismiss --store /s", "help[1]:\n  "+strings.Repeat("word ", 40)+"\n", 0)
		g.feed(jig2Mid()...).press("enter", "down", "d")
		got := lastPanel(g.view())
		if len(got) != 1 || !strings.HasSuffix(got[0], "…") || len([]rune(got[0])) > 80 {
			t.Errorf("panel = %q, want one line of at most 80 cells ending in an ellipsis", got)
		}
	})
}

// TestLastCommandPanelExpiresWhenTheTicketMoves: the line of a refused gate is
// gone once the ticket reads differently (a round opened), stays while nothing
// changed, and goes on leaving the screen.
func TestLastCommandPanelExpiresWhenTheTicketMoves(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.r.answer("gate JIG-2 --store /s", "error: round 1 is not recorded\ncode: GATE_NOT_GREEN\n", 1)
	g.feed(jig2Mid()...).press("enter", "g")
	if len(lastPanel(g.view())) != 1 {
		t.Fatalf("no line after the refused gate:\n%s", g.view())
	}

	// A line that moves nothing of the phases leaves it there.
	g.feed(jl(at(11, 40), "JIG-2", "", journal.EventNote, with("s-person", "", "", "a note", "", "")))
	if len(lastPanel(g.view())) != 1 {
		t.Errorf("the line went with nothing changed:\n%s", g.view())
	}

	// The round opened: the ticket's phases read differently, the line is gone.
	g.feed(jl(at(12, 0), "JIG-2", "", journal.EventGateOpen, func(l *journal.Line) { l.Attempt = 2 }))
	if got := lastPanel(g.view()); len(got) != 0 {
		t.Errorf("the line outlived the ticket's move: %q", got)
	}

	// A notice keeps its own rules: it is not a command's line.
	g.m.notice = "tickets: could not read"
	g.feed(jl(at(12, 30), "JIG-2", "", journal.EventNote, with("s-person", "", "", "another", "", "")))
	if !strings.Contains(g.view(), "tickets: could not read") {
		t.Errorf("the notice went with the line:\n%s", g.view())
	}
}

// TestAChartCommandLineExpiresWithTheChartsStanding: a command with no ticket,
// made on a chart's screen, is gone once the chart's verdict reads differently.
func TestAChartCommandLineExpiresWithTheChartsStanding(t *testing.T) {
	t.Parallel()
	g := chartScreenRig(t)
	c := g.m.w.charts[g.m.top().chart]
	if c == nil || c.info == nil {
		t.Fatal("the chart screen has no chart read")
	}
	g.m.last = &outcome{Steps: []ranStep{{Args: []string{"next", c.info.Name}, Out: "ok\n"}}, Print: g.m.standingPrint("")}
	g.m.expireLast()
	if g.m.last == nil {
		t.Fatal("the line went with the chart unchanged")
	}
	c.info.Prompt += " again"
	g.m.expireLast()
	if g.m.last != nil {
		t.Errorf("the line outlived the chart's verdict: %+v", g.m.last)
	}
}
