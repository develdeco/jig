package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// stepsOf feeds lines for ticket with status, and returns its steps.
func stepsOf(t *testing.T, ticket, status string, lines ...events.Envelope) []stepRow {
	t.Helper()
	g := newRig(t, ticket)
	g.r.answer("status "+ticket+" --store /s", status, 0)
	g.feed(lines...)
	return g.m.steps(g.m.w.tickets[ticket])
}

func TestAnAdoptedBranchIsBuiltAndGoesOnToReview(t *testing.T) {
	t.Parallel()
	status := strings.Join([]string{"ticket: JIG-5", "branch: feature/x", "state: green",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, nil), "questions: none"}, "\n") + "\n"
	steps := stepsOf(t, "JIG-5", status,
		jl(at(20, 0), "JIG-5", "", journal.EventGateOpen),
		jl(at(20, 30), "JIG-5", "", journal.EventGateClean, func(l *journal.Line) { l.Attempt, l.Session = 1, "s-reviewer" }),
	)
	for i := stepBrief; i <= stepReview; i++ {
		if !steps[i].Done {
			t.Errorf("%s is not done: %+v", stepNames[i], steps[i])
		}
	}
	if cur := currentStep(steps); cur != stepPublish || steps[stepPublish].State != StateReady {
		t.Errorf("current step = %s %+v, want the publish, ready", stepNames[cur], steps[cur])
	}
}

func TestFixesGreenAfterARoundAskForTheNextRound(t *testing.T) {
	t.Parallel()
	status := strings.Join([]string{"ticket: JIG-6", "state: green",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"a", "green", "1", "-", "-", "Do a"}, {"fix-1", "green", "1", "-", "-", "Fix it"},
		}), "questions: none"}, "\n") + "\n"
	steps := stepsOf(t, "JIG-6", status,
		jl(at(30, 0), "JIG-6", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(30, 10), "JIG-6", "a", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		jl(at(31, 0), "JIG-6", "", journal.EventGateOpen),
		jl(at(31, 30), "JIG-6", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = 1, "s-reviewer" }),
		jl(at(31, 31), "JIG-6", "fix-1", journal.EventFixSlice),
		jl(at(32, 0), "JIG-6", "fix-1", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(32, 10), "JIG-6", "fix-1", journal.EventVerified, with("", sha('b'), "", "", "", "")),
	)
	r := steps[stepReview]
	if r.Done || currentStep(steps) != stepReview || r.Text != "round 1's fixes are green · g runs round 2" {
		t.Errorf("review = %+v, want the current step asking for round 2", r)
	}
}

func TestAStepIsNotDoneBeforeTheStepsAheadOfIt(t *testing.T) {
	t.Parallel()
	status := strings.Join([]string{"ticket: JIG-7", "state: green",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{{"a", "green", "1", "-", "-", "Do a"}}),
		"questions: none",
		axi.KV("brief", [][2]string{{"approval", "changed"}, {"approved_by", "Ada"}, {"approved_at", "2026-10-23T09:00:00Z"}}),
	}, "\n") + "\n"
	steps := stepsOf(t, "JIG-7", status,
		jl(at(40, 0), "JIG-7", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(40, 10), "JIG-7", "a", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		jl(at(41, 0), "JIG-7", "", journal.EventGateOpen),
		jl(at(41, 30), "JIG-7", "", journal.EventGateClean, func(l *journal.Line) { l.Attempt, l.Session = 1, "s-reviewer" }),
	)
	if cur := currentStep(steps); cur != stepBrief {
		t.Fatalf("current step = %s, want the brief: it changed after its approval", stepNames[cur])
	}
	for i := stepBuild; i <= stepPublish; i++ {
		if steps[i].Done {
			t.Errorf("%s is done ahead of the brief: %+v", stepNames[i], steps[i])
		}
	}
}
