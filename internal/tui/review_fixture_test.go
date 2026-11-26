package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// reviewFixRoundEvents is JIG-2 in its fix round: round 1 raised findings and
// sent a fix slice back, the fix was built, and round 2 raised more, with a fix
// slice sent back and an ask that waits for a person.
func reviewFixRoundEvents() []events.Envelope {
	return append(jig2Mid(),
		jl(at(12, 10), "JIG-2", "fix-1-alpha", journal.EventFixSlice, func(l *journal.Line) { l.Attempt = 1 }),
		jl(at(12, 20), "JIG-2", "fix-1-alpha", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(12, 40), "JIG-2", "fix-1-alpha", journal.EventVerified, with("", sha('d'), "", "", "", "")),
		jl(at(13, 0), "JIG-2", "", journal.EventGateOpen),
		jl(at(13, 30), "JIG-2", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = 2, "s-reviewer" }),
		jl(at(13, 40), "JIG-2", "fix-2-alpha", journal.EventFixSlice, func(l *journal.Line) { l.Attempt = 2 }),
	)
}

// reviewFixRoundStatus is `jig status JIG-2` for reviewFixRoundEvents: two rounds
// that found things, the findings of the second (a fix, an ask and a note), the
// fix slice each round sent back and the ask that waits.
func reviewFixRoundStatus() string {
	return strings.Join([]string{
		"ticket: JIG-2",
		"state: parked",
		"fix_budget: 2 of 3 rounds used",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal", "workspace", "oracle", "from_gate", "findings"}, [][]string{
			{"a", "green", "1", "-", "-", "Load the config", "alpha", "unit", "-", "-"},
			{"fix-1-alpha", "green", "1", "-", "-", "Fix these gate findings:\n\nalpha/a.go:12 Close the file", "alpha", "unit", "1", "r1-f3"},
			{"fix-2-alpha", "queued", "0", "-", "-", "Fix these gate findings:\n\nalpha/c.go:7 Check the returned error", "alpha", "unit", "2", "r2-f1"},
		}),
		"questions: none",
		axi.Table("outstanding_asks", []string{"id", "risk", "file:line", "title", "why"}, [][]string{
			{"r2-f2", "high", "alpha/c.go:30", "Retry loop never ends", "the fix budget is not reached, so a person decides"},
		}),
		axi.Table("rounds", roundColumns, [][]string{
			{"1", "findings", "s-reviewer", "claude", "claude-opus-5", "true", "1", "1", "1", "full", sha('0'), sha('c'), "4", "0", "-", "-"},
			{"2", "findings", "s-reviewer", "claude", "claude-opus-5", "true", "1", "1", "1", "delta", sha('c'), sha('d'), "3", "0", "-", "-"},
		}),
		axi.Table("findings", findingColumns, [][]string{
			{"r2-f1", "2", "fix", "medium", "alpha/c.go:7", "Check the returned error", "open", "the error is dropped and the caller carries on", "", ""},
			{"r2-f2", "2", "ask", "high", "alpha/c.go:30", "Retry loop never ends", "asked", "a failing call is retried forever", "", ""},
			{"r2-f3", "2", "note", "low", "alpha/c.go:2", "Rename the helper", "noted", "a clearer name, whenever the file is next touched", "", ""},
		}),
		axi.Help("Run `jig triage JIG-2 <id> keep|dismiss` to decide the outstanding asks"),
	}, "\n") + "\n"
}

// reviewFixRound is the review screen of the ticket in its fix round at w x h:
// the cursor is on the ask, which is where opening the review puts it.
func reviewFixRound(t *testing.T, w, h int) *rig {
	t.Helper()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", reviewFixRoundStatus(), 0)
	g.feed(reviewFixRoundEvents()...).press("3")
	if g.m.Screen() != "review" {
		t.Fatalf("on %s, want review", g.m.Screen())
	}
	return g.sized(w, h)
}
