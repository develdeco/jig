package tui

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestActivityShowsAFinalRun: the line `jig verify --final` records reads as
// the ticket's final run with its command, outcome and seconds, is drawn
// green for a covering pass and stalled for a failure or a run that covers
// nothing, and never reads as a bare "final-oracle pass".
func TestActivityShowsAFinalRun(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-6")
	tk := ticketOf("JIG-6")
	g.m.w.tickets["JIG-6"] = tk
	passed := jl(at(3, 1), "JIG-6", "", journal.EventFinalOracle, with("", sha('a'), "pass", "", "", ""), func(l *journal.Line) {
		l.Command, l.Seconds = "(go test ./a) && (go test ./b)", 9
	})
	failed := jl(at(3, 2), "JIG-6", "", journal.EventFinalOracle, with("", sha('b'), "fail", "", "", ""), func(l *journal.Line) {
		l.Command, l.Seconds, l.Summary = "go test ./a", 4, "red on purpose"
	})
	uncovered := jl(at(3, 3), "JIG-6", "", journal.EventFinalOracle, with("", sha('c'), "pass", "", "", ""), func(l *journal.Line) {
		l.Command, l.Seconds, l.Uncovered = "go test ./a", 2, journal.UncoveredDirtyAfter
	})
	multi := jl(at(3, 4), "JIG-6", "", journal.EventFinalOracle, with("", sha('d'), "pass", "", "", ""), func(l *journal.Line) {
		l.Command, l.Seconds = "(\necho one # note\n) && (echo two)", 5
	})
	tk.lines = append(tk.lines, passed, failed, uncovered, multi)
	if got := g.m.say(tk, multi); got != "the final run passed in 5s" {
		t.Errorf("a final run of a command on several lines says %q, want the command left out: it would take several rows", got)
	}

	if got := g.m.say(tk, passed); got != "the final run `(go test ./a) && (go test ./b)` passed in 9s" {
		t.Errorf("a final run says %q", got)
	}
	if got := g.m.say(tk, failed); got != "the final run `go test ./a` failed in 4s: red on purpose" {
		t.Errorf("a failed final run says %q", got)
	}
	if got := g.m.say(tk, uncovered); got != "the final run `go test ./a` passed in 2s (covers nothing: tree dirty after)" {
		t.Errorf("an uncovered final run says %q", got)
	}
	if got := eventState(passed); got != StateGreen {
		t.Errorf("a covering final pass is drawn %q, want %q", got, StateGreen)
	}
	if got := eventState(failed); got != StateStalled {
		t.Errorf("a failed final run is drawn %q, want %q", got, StateStalled)
	}
	if got := eventState(uncovered); got != StateStalled {
		t.Errorf("an uncovered final run is drawn %q, want %q", got, StateStalled)
	}
	if got := describe(passed); got != "final run pass "+short(sha('a')) {
		t.Errorf("describe = %q", got)
	}
}
