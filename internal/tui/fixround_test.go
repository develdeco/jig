package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// fixSliceStatus is a status listing two fix slices, queued by rounds 1 and 2;
// without from_gate it is a status from before the column was carried.
func fixSliceStatus(fromGate bool) string {
	cols := []string{"id", "state", "attempts", "blocked_by", "question", "goal"}
	rows := [][]string{
		{"a", "green", "1", "-", "-", "Do a"},
		{"fix-1", "green", "1", "-", "-", "Fix round one"},
		{"fix-2", "queued", "0", "-", "-", "Fix round two"},
	}
	if fromGate {
		cols = append(cols, "from_gate")
		rows[0] = append(rows[0], "-")
		rows[1] = append(rows[1], "1")
		rows[2] = append(rows[2], "2")
	}
	return strings.Join([]string{"ticket: JIG-6", "state: building", axi.Table("slices", cols, rows), "questions: none"}, "\n") + "\n"
}

// fixSliceLines are the lines of two rounds that each sent a fix slice back,
// the fix-slice lines carrying no attempt as jig writes them.
func fixSliceLines() []events.Envelope {
	return []events.Envelope{
		jl(at(30, 0), "JIG-6", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(30, 10), "JIG-6", "a", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		jl(at(31, 0), "JIG-6", "", journal.EventGateOpen),
		jl(at(31, 30), "JIG-6", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = 1, "s-reviewer" }),
		jl(at(31, 31), "JIG-6", "fix-1", journal.EventFixSlice),
		jl(at(32, 0), "JIG-6", "", journal.EventGateOpen),
		jl(at(32, 30), "JIG-6", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = 2, "s-reviewer" }),
		jl(at(32, 31), "JIG-6", "fix-2", journal.EventFixSlice),
	}
}

// TestFixRowsRoundFromGate: a fix slice's row on the build screen reads the round that queued it, from
// the status's from_gate and from the journal alone, and two fix slices of
// different rounds each read their own.
func TestFixRowsRoundFromGate(t *testing.T) {
	t.Parallel()
	for name, fromGate := range map[string]bool{"status": true, "lines alone": false} {
		g := newRig(t, "JIG-6")
		g.r.answer("status JIG-6 --store /s", fixSliceStatus(fromGate), 0)
		g.feed(fixSliceLines()...)
		g.press("enter") // the build screen: its rows read a fix slice by its round
		v := readWords(g.view())
		for _, want := range []string{"fix-1 green 1 try round 1 · Fix round one", "fix-2 queued 0 tries round 2 · Fix round two"} {
			if !strings.Contains(v, want) {
				t.Errorf("%s: the build screen lacks %q:\n%s", name, want, v)
			}
		}
	}
}

// TestActivityFixSliceRound: the activity says which round sent a fix slice
// back, and names none for a slice no source places.
func TestActivityFixSliceRound(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-6")
	g.r.answer("status JIG-6 --store /s", fixSliceStatus(true), 0)
	lines := fixSliceLines()
	g.feed(lines...)
	tk := g.m.w.tickets["JIG-6"]
	for _, tc := range []struct {
		l    events.Envelope
		want string
	}{
		{lines[4], "sent back fix-1 from round 1: Fix round one"},
		{lines[7], "sent back fix-2 from round 2: Fix round two"},
	} {
		if got := g.m.say(tk, tc.l); got != tc.want {
			t.Errorf("activity = %q, want %q", got, tc.want)
		}
	}
	// No round closed before it and no status: no round is invented.
	bare := ticketOf("JIG-6")
	orphan := jl(at(3, 5), "JIG-6", "fix-9", journal.EventFixSlice)
	bare.lines = append(bare.lines, orphan)
	if got := g.m.say(bare, orphan); got != "sent back fix-9" {
		t.Errorf("an unplaced fix slice says %q, want no round word", got)
	}
}
