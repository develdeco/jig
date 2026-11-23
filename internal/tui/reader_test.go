package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// TestASubagentSlicesCallsAfterItsQuestionAreNoWork: a slice held by a subagent
// that worked nine minutes, asked, and went on calling for ten more while the
// question stood: those calls are no work, before the answer and after it.
func TestASubagentSlicesCallsAfterItsQuestionAreNoWork(t *testing.T) {
	t.Parallel()
	asked := strings.Join([]string{"ticket: SHOP-1", "state: parked",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"c", "needs-input", "1", "-", "q-001", "Pick the tone"},
		}),
		axi.Table("questions", []string{"id", "slice", "status"}, [][]string{{"q-001", "c", "open"}}),
	}, "\n") + "\n"
	g := newRig(t, "SHOP-1")
	atClock(g, "2026-10-23T09:25:00Z")
	g.r.answer("status SHOP-1 --store /s", asked, 0)
	agent := func(l *journal.Line) { l.Agent = "b1" }
	g.feed(jl(at(1, 0), "SHOP-1", "c", journal.EventClaim, with("s-b", "", "", "", "", ""), agent))
	for m := 2; m <= 9; m++ {
		g.feed(toolOn("SHOP-1", at(m, 0), "s-b", "go test ./...", agent))
	}
	g.feed(jl(at(10, 0), "SHOP-1", "c", journal.EventQuestion, with("s-b", "", "", "Formal or casual?", "q-001", "agent"), agent))
	g.feed(toolOn("SHOP-1", at(10, 0), "s-b", "jig ask --slice c", agent)) // the call that wrote the question
	for m := 11; m <= 20; m++ {
		g.feed(toolOn("SHOP-1", at(m, 0), "s-b", "go test ./...", agent))
	}
	tk := g.m.w.tickets["SHOP-1"]
	if got := g.m.steps(tk)[stepBuild].Took; got != "9m 00s" {
		t.Errorf("while the question stood the build took %q, want the 9 minutes before it", got)
	}

	atClock(g, "2026-10-23T09:21:00Z")
	g.r.answer("status SHOP-1 --store /s", strings.Join([]string{"ticket: SHOP-1", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"c", "building", "1", "-", "-", "Pick the tone"},
		}), "questions: none"}, "\n")+"\n", 0)
	g.feed(jl(at(21, 0), "SHOP-1", "c", journal.EventAnswer, with("", "", "", "Casual.", "q-001", "person")))
	tk = g.m.w.tickets["SHOP-1"]
	if got := g.m.steps(tk)[stepBuild].Took; got != "9m 00s" {
		t.Errorf("after the answer the build took %q, want the same 9 minutes", got)
	}
	for _, p := range g.m.placed(tk, g.m.phaseOf(tk)) {
		if p.l.Command == "go test ./..." && p.key == "slice:c" && p.l.TS > at(10, 59) && p.l.TS < at(21, 0) {
			t.Errorf("the call at %s, made while the question stood, is held by slice c once it is answered", p.l.TS)
		}
	}
}

// TestAStepRowIsTheSumOfItsSubSteps: every counted gap belongs to the one
// sub-step whose line closes it, so a row is the sum of the times its
// sub-steps read; the drafted brief and two slices with a gap between them.
func TestAStepRowIsTheSumOfItsSubSteps(t *testing.T) {
	t.Parallel()
	sumOf := func(g *rig, step int) (time.Duration, time.Duration) {
		tk := g.m.w.tickets["SHOP-1"]
		d := g.m.phaseOf(tk)
		c := g.m.clocks(tk, d)
		var sum time.Duration
		for _, s := range d.Phases[step].Subs {
			sum += c.work.of(step, s.Key)
		}
		return sum, g.m.steps(tk)[step].took
	}
	g := draftedRig(t)
	if sum, row := sumOf(g, stepBrief); sum != row || row != 6*time.Second {
		t.Errorf("the brief's sub-steps add up to %v and its row says %v, want 6s both", sum, row)
	}

	status := strings.Join([]string{"ticket: SHOP-1", "state: green",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"a", "green", "1", "-", "-", "Do a"}, {"b", "green", "1", "-", "-", "Do b"},
		}), "questions: none"}, "\n") + "\n"
	b := newRig(t, "SHOP-1")
	atClock(b, "2026-10-23T09:30:00Z")
	b.r.answer("status SHOP-1 --store /s", status, 0)
	b.feed(
		jl(at(0, 0), "SHOP-1", "a", journal.EventClaim, with("s-b", "", "", "", "", "")),
		jl(at(0, 40), "SHOP-1", "a", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		toolOn("SHOP-1", at(1, 0), "s-b", "git status", said("Look at the change")),
		jl(at(1, 30), "SHOP-1", "b", journal.EventClaim, with("s-b", "", "", "", "", "")),
		jl(at(2, 0), "SHOP-1", "b", journal.EventVerified, with("", sha('b'), "", "", "", "")),
	)
	if sum, row := sumOf(b, stepBuild); sum != row || row != 2*time.Minute {
		t.Errorf("the build's slices add up to %v and its row says %v, want 2m both", sum, row)
	}
	_ = phase.Build
}

// TestTheAsksWaitClosesWhenTheAsksAreDecided: the asks a round left waited for
// the person from the round's close to the last decision; the review reads that
// as a wait that ended, and its time is not the review's work.
func TestTheAsksWaitClosesWhenTheAsksAreDecided(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	atClock(g, "2026-10-23T09:20:00Z")
	g.r.answer("status JIG-2 --store /s", statusJIG2Done(), 0)
	g.feed(jig2()...)
	tk := g.m.w.tickets["JIG-2"]
	d := g.m.phaseOf(tk)
	w, ok := g.m.clocks(tk, d).waitOf(stepReview, "asks")
	if !ok || w.standing() || w.end.Sub(w.start) != 35*time.Second || w.forWhom != phase.WaitsForPerson {
		t.Errorf("the asks' wait = %+v (%v), want one of 35s that ended, for the person", w, ok)
	}
	if got := g.m.steps(tk)[stepReview].Took; got != "1m 55s" {
		t.Errorf("the review took %q, want its work without the wait", got)
	}
}
