package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// keptFixLines are two rounds' worth of lines: round 1 routes fix-1, and a
// kept ask of round 1 is queued as fix-2 by the next gate, which journals
// keep-queued after the fix-slice line, before round 2 opens.
func keptFixLines() []events.Envelope {
	return []events.Envelope{
		jl(at(30, 0), "JIG-6", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(30, 10), "JIG-6", "a", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		jl(at(31, 0), "JIG-6", "", journal.EventGateOpen),
		jl(at(31, 30), "JIG-6", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = 1, "s-reviewer" }),
		jl(at(31, 31), "JIG-6", "fix-1", journal.EventFixSlice),
		jl(at(31, 32), "JIG-6", "", journal.EventRoute),
		jl(at(32, 0), "JIG-6", "fix-2", journal.EventFixSlice),
		jl(at(32, 1), "JIG-6", "", journal.EventKeepQueued, func(l *journal.Line) { l.Ref = "r1-f2" }),
	}
}

// TestFixRoundFromJournalEqualsFromGate: with the journal alone a fix slice a
// kept ask queued reads the round the status gives it as from_gate, on the
// review screen and in the sub-steps.
func TestFixRoundFromJournalEqualsFromGate(t *testing.T) {
	t.Parallel()
	statusOf := func(fromGate bool) string {
		s := fixSliceStatus(fromGate)
		return s
	}
	var views [2]string
	for i, fromGate := range []bool{true, false} {
		g := newRig(t, "JIG-6")
		g.r.answer("status JIG-6 --store /s", statusOf(fromGate), 0)
		g.feed(keptFixLines()...)
		tk := g.m.w.tickets["JIG-6"]
		if got := tk.fixRound("fix-2"); got != 2 {
			t.Errorf("fromGate=%v: fix-2 reads round %d, want 2", fromGate, got)
		}
		g.press("down", "enter")
		views[i] = g.view()
		for _, want := range []string{"fix-1  round 1  Fix round one", "fix-2  round 2  Fix round two"} {
			if !strings.Contains(views[i], want) {
				t.Errorf("fromGate=%v: the review screen lacks %q:\n%s", fromGate, want, views[i])
			}
		}
	}
}

// TestFixSliceUnplacedNamesNoRound: a fix slice nothing places in a round is
// still a fix slice, and the sub-steps name no round for it, as the activity
// names none.
func TestFixSliceUnplacedNamesNoRound(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-6")
	g.feed(
		jl(at(30, 0), "JIG-6", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(30, 10), "JIG-6", "fix-9", journal.EventFixSlice),
		jl(at(30, 11), "JIG-6", "fix-9", journal.EventClaim, with("s-builder", "", "", "", "", "")),
	)
	tk := g.m.w.tickets["JIG-6"]
	labels := g.m.subLabelsOf(tk)
	if got := labels["slice:fix-9"]; got != "fix slice fix-9" {
		t.Errorf("the sub-step names %q, want fix slice fix-9 with no round", got)
	}
	if got := labels["slice:a"]; got != "slice a" {
		t.Errorf("a planned slice is named %q, want slice a", got)
	}
}

// TestReviewerOwnChangeSaysItOnce: the review row of a round that is not
// independent names its reviewer "builder reviewing its own change" and does
// not say it a second time.
func TestReviewerOwnChangeSaysItOnce(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", reviewStatus([][]string{
		{"1", "not-independent", "s-reviewer", "codex", "gpt-5", "false", "0", "1", "0", "full", sha('0'), sha('c'), "4"},
	}, nil), 0)
	g.feed(append(jig2Mid(),
		jl(at(12, 30), "JIG-2", "", journal.EventGateOpen),
		jl(at(12, 50), "JIG-2", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = 1, "s-reviewer" }),
	)...).press("enter")
	text := pageText(g)
	text, _, _ = strings.Cut(text, "asks")
	if n := strings.Count(text, "builder reviewing its own change"); n != 1 {
		t.Errorf("the row says it %d times, want once:\n%s", n, text)
	}
	if strings.Contains(text, "NOT independent") {
		t.Errorf("the row repeats itself with NOT independent:\n%s", text)
	}
}
