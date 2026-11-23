package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// lastPanel is the lines of the frame between the screen's last box and its
// keys: what the panel printed for the last command.
func lastPanel(view string) []string {
	var out []string
	for _, l := range strings.Split(view, "\n") {
		if strings.HasPrefix(l, "  ✓ jig") || strings.HasPrefix(l, "  ✗ jig") {
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
		g := newQuietRig(t, "JIG-2")
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
		g := newQuietRig(t, "JIG-2")
		g.r.answer("gate JIG-2 --store /s", "error: round 1 is not recorded\ncode: GATE_NOT_GREEN\nhelp[1]:\n  Run `jig next JIG-2`\n", 1)
		g.feed(jig2Mid()...).press("enter", "g")
		got := lastPanel(g.view())
		if len(got) != 1 || got[0] != "  ✗ jig gate JIG-2  round 1 is not recorded (GATE_NOT_GREEN)" {
			t.Errorf("panel = %q", got)
		}
	})

	t.Run("a chain whose second step failed shows the failure", func(t *testing.T) {
		g := newQuietRig(t, "JIG-2")
		g.r.answer("publish JIG-2 --yes --store /s", "error: the head moved\ncode: PUBLISH_STALE\n", 1)
		g.feed(jig2Mid()...).press("p", "y")
		got := lastPanel(g.view())
		if len(got) != 1 || got[0] != "  ✗ jig publish JIG-2 --yes  the head moved (PUBLISH_STALE)" {
			t.Errorf("panel = %q, want only the failed step", got)
		}
	})

	t.Run("a long sentence is cut", func(t *testing.T) {
		g := newQuietRig(t, "JIG-2")
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
	g := newQuietRig(t, "JIG-2")
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

// TestACommandsOwnEffectDoesNotRemoveItsLine: the refusal a gate journals is part
// of the standing its line is measured against, whether it arrives while the
// command runs or after its result; only a later move of the ticket removes the
// line.
func TestACommandsOwnEffectDoesNotRemoveItsLine(t *testing.T) {
	t.Parallel()
	g := newQuietRig(t, "JIG-2")
	g.r.answer("gate JIG-2 --store /s", "error: round 1 is not recorded\ncode: GATE_NOT_GREEN\n", 1)
	g.feed(jig2Mid()...)
	refusal := func(sec int) {
		g.feed(jl(at(12, sec), "JIG-2", "", journal.EventGateRefused, func(l *journal.Line) {
			l.Code, l.Summary = "GATE_NOT_GREEN", "slice a is queued, not green"
		}))
	}
	cmd := g.m.exec("gate", "JIG-2", g.m.verbArgs("gate", "JIG-2"))
	refusal(0) // journaled while the command runs
	g.settle(cmd)
	if len(lastPanel(g.view())) != 1 {
		t.Fatalf("the refused gate's line is not drawn:\n%s", g.view())
	}
	refusal(5) // read again after the result
	if len(lastPanel(g.view())) != 1 {
		t.Errorf("the line went with the command's own refusal:\n%s", g.view())
	}
	g.feed(jl(at(13, 0), "JIG-2", "", journal.EventGateOpen, func(l *journal.Line) { l.Attempt = 2 }))
	if got := lastPanel(g.view()); len(got) != 0 {
		t.Errorf("the line outlived a later move of the ticket: %q", got)
	}
}

// TestTheLineIsFixedByTheFirstRosterReadAfterTheResult: in the program's order of
// messages (the result, its status read, the refusal it journaled, then the
// roster read that carries the digest that says so) the line stays, and goes at
// a later move. A roster read that was already out when the result arrived does
// not fix it; one that began after does. Driven through Update with no rig sync.
func TestTheLineIsFixedByTheFirstRosterReadAfterTheResult(t *testing.T) {
	t.Parallel()
	refused := func(g *rig, text string) rosterMsg {
		d := *g.m.w.tickets["JIG-2"].digest
		d.Phases = append([]digestPhase(nil), d.Phases...)
		for i := range d.Phases {
			if d.Phases[i].Name == "review" {
				d.Phases[i].Text = text
			}
		}
		return rosterMsg{rows: []rosterRow{{ID: "JIG-2", Title: "Config loader", State: "ready", Digest: &d}}, seq: g.m.rosterStarted}
	}
	ran := func(g *rig) {
		g.m.Update(ranMsg{result: outcome{Label: "gate", Ticket: "JIG-2", Steps: []ranStep{{
			Args: g.m.verbArgs("gate", "JIG-2"), Out: "error: round 1 is not recorded\ncode: GATE_NOT_GREEN\n", Code: 1}}}})
	}
	refusal := jl(at(12, 0), "JIG-2", "", journal.EventGateRefused, func(l *journal.Line) { l.Code, l.Summary = "GATE_NOT_GREEN", "slice a is queued" })
	shown := func(g *rig) int { return len(lastPanel(g.view())) }

	t.Run("the roster read that follows", func(t *testing.T) {
		g := newQuietRig(t, "JIG-2")
		g.feed(jig2Mid()...)
		ran(g)
		g.m.Update(statusMsg{ticket: "JIG-2", info: parseStatus(statusJIG2())})
		g.m.Update(eventsMsg{refusal})
		if shown(g) != 1 {
			t.Fatalf("the line is gone before any roster read:\n%s", g.view())
		}
		g.m.Update(refused(g, "gate refused: slice a is queued"))
		if shown(g) != 1 {
			t.Fatalf("the roster read that carries the command's own refusal removed the line:\n%s", g.view())
		}
		g.m.Update(refused(g, "round 2 open · waiting for a reviewer"))
		if shown(g) != 0 {
			t.Errorf("the line outlived a later move of the ticket:\n%s", g.view())
		}
	})

	t.Run("a roster read that was already out", func(t *testing.T) {
		g := newQuietRig(t, "JIG-2")
		g.feed(jig2Mid()...)
		out := g.m.rosterStarted
		ran(g)
		stale := refused(g, "round 1: 2 asks wait for you")
		stale.seq = out
		_, cmd := g.m.Update(stale)
		if cmd == nil {
			t.Errorf("a read from before the result did not ask for one after it")
		}
		if g.m.last == nil || g.m.last.Fixed {
			t.Fatalf("a read from before the result fixed the line: %+v", g.m.last)
		}
		g.m.Update(refused(g, "gate refused: slice a is queued"))
		if g.m.last == nil || !g.m.last.Fixed || shown(g) != 1 {
			t.Errorf("the read after the result did not fix the line: %+v", g.m.last)
		}
	})
}

// TestTheReadsAResultAsksForAreTheOnesThatFixTheLine: the reads run through the
// fake runner as the program runs them. A roster read already out when the
// result arrives does not fix the line and asks for another; the result asks for
// the ticket's status and the roster; the roster read that begins after fixes the
// line and frees the reader. A failed read, or a chart's, fixes no ticket's line.
func TestTheReadsAResultAsksForAreTheOnesThatFixTheLine(t *testing.T) {
	t.Parallel()
	g := newQuietRig(t, "JIG-2")
	g.r.answer("gate JIG-2 --store /s", "error: round 1 is not recorded\ncode: GATE_NOT_GREEN\n", 1)
	g.feed(jig2Mid()...)

	out := g.m.loadRoster() // a read begun before the gate ran
	if out == nil {
		t.Fatal("no roster read began")
	}
	ran := g.m.exec("gate", "JIG-2", g.m.verbArgs("gate", "JIG-2"))().(ranMsg)
	_, asked := g.m.Update(ran)
	if g.m.last == nil || g.m.last.Fixed || g.m.last.After != g.m.rosterStarted {
		t.Fatalf("the result's line = %+v, want it unfixed after read %d", g.m.last, g.m.rosterStarted)
	}
	if !g.m.rosterBusy {
		t.Fatalf("the read out before the result is not the one in flight")
	}

	// A chart's read and a failed roster read fix no ticket's line.
	g.m.Update(chartMsg{name: "elsewhere"})
	g.m.Update(rosterMsg{err: errors.New("no store"), seq: g.m.rosterStarted + 5})
	if g.m.last.Fixed {
		t.Fatalf("a chart read or a failed roster read fixed a ticket's line")
	}

	// The read from before the result lands: it does not fix the line, and
	// another begins.
	_, again := g.m.Update(out().(rosterMsg))
	if g.m.last.Fixed || again == nil || !g.m.rosterBusy {
		t.Fatalf("the read from before the result fixed the line or asked for no other: fixed %v, busy %v", g.m.last.Fixed, g.m.rosterBusy)
	}

	// The reads the result asked for, and then the new roster read, run.
	before := len(g.r.calls)
	g.settle(asked)
	g.settle(again)
	var ran2 []string
	for _, c := range g.r.calls[before:] {
		ran2 = append(ran2, c.Line)
	}
	if !slices.Contains(ran2, "status JIG-2 --store /s") || !slices.Contains(ran2, "next --json --store /s") {
		t.Errorf("the reads run after the gate = %q, want the ticket's status and the roster", ran2)
	}
	if g.m.last == nil || !g.m.last.Fixed || g.m.rosterBusy {
		t.Errorf("the roster read that began after the result did not fix the line: %+v, busy %v", g.m.last, g.m.rosterBusy)
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
	g.m.last = &outcome{Steps: []ranStep{{Args: []string{"next", c.info.Name}, Out: "ok\n"}}, Print: g.m.standingPrint(""), Fixed: true}
	g.m.settleLast(nil)
	if g.m.last == nil {
		t.Fatal("the line went with the chart unchanged")
	}
	c.info.Prompt += " again"
	g.m.settleLast(nil)
	if g.m.last != nil {
		t.Errorf("the line outlived the chart's verdict: %+v", g.m.last)
	}
}
