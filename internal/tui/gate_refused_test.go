package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
)

// TestReviewStepGateRefused: a ticket whose gate refused before opening a round
// says so on the review step and on the review screen, has a row for it in the
// activity, none in the round list, and reads as an open round once a gate-open
// follows.
func TestReviewStepGateRefused(t *testing.T) {
	t.Parallel()
	status := strings.Join([]string{"ticket: JIG-8", "state: green",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"a", "green", "1", "-", "-", "Do a"},
		}), "questions: none"}, "\n") + "\n"
	g := newRig(t, "JIG-8")
	g.r.answer("status JIG-8 --store /s", status, 0)
	refused := jl(at(40, 0), "JIG-8", "", journal.EventGateRefused, func(l *journal.Line) {
		l.Code, l.Summary = "INTENT_CONFLICT", "ticket JIG-8 already has a brief.md"
	})
	g.feed(
		jl(at(39, 0), "JIG-8", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(39, 10), "JIG-8", "a", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		refused,
	)
	tk := g.m.w.tickets["JIG-8"]
	const want = "gate refused: ticket JIG-8 already has a brief.md"
	if r := g.m.steps(tk)[stepReview]; r.Text != want {
		t.Errorf("review step = %+v, want %q", r, want)
	}
	if rows := tk.rounds(g.m.zone); len(rows) != 0 {
		t.Errorf("rounds = %+v, want no row for a refusal", rows)
	}
	if stepOfLine(refused) != stepReview || eventState(refused) != StateReviewing {
		t.Errorf("the refusal is in step %d, state %q, want the review's, in the gate's state", stepOfLine(refused), eventState(refused))
	}
	if got := g.m.activityLine(tk, g.m.subLabelsOf(tk), refused); !strings.HasSuffix(got, "review · "+want) {
		t.Errorf("activity row = %q, want it to end with the review and %q", got, want)
	}
	if got := describe(refused); got != want {
		t.Errorf("describe = %q, want %q", got, want)
	}

	g.press("enter")
	if g.m.Screen() != "review" {
		t.Fatalf("on %s, want the review screen", g.m.Screen())
	}
	if text := pageText(g); !strings.Contains(text, want) || strings.Contains(text, "round 1  open") {
		t.Errorf("review screen:\n%s", text)
	}

	g.feed(jl(at(41, 0), "JIG-8", "", journal.EventGateOpen, func(l *journal.Line) { l.Attempt = 1 }))
	if r := g.m.steps(g.m.w.tickets["JIG-8"])[stepReview]; r.Text != "round 1 open · waiting for a reviewer" {
		t.Errorf("review once a round opened = %+v", r)
	}
	if text := pageText(g); !strings.Contains(text, "round 1  open") {
		t.Errorf("review screen once a round opened:\n%s", text)
	}
}
