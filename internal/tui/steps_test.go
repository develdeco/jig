package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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

func TestAnAdoptedBranchSeenWithoutItsStatusIsBuilt(t *testing.T) {
	t.Parallel()
	g := newRig(t, "") // the main screen reads no ticket's status
	g.feed(
		jl(at(20, 0), "JIG-5", "", journal.EventGateOpen),
		jl(at(20, 30), "JIG-5", "", journal.EventGateClean, func(l *journal.Line) { l.Attempt, l.Session = 1, "s-reviewer" }),
	)
	steps := g.m.steps(g.m.w.tickets["JIG-5"])
	if !steps[stepBuild].Done || !steps[stepReview].Done || currentStep(steps) != stepPublish {
		t.Errorf("steps = %+v, want the build and the review done and the publish current", steps)
	}
}

func TestTheLastRowShowsThePanelBelowIt(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.send(tea.WindowSizeMsg{Width: 80, Height: 18})
	g.feed(jig1()...).press("enter", "end") // the build's screen, its last slice
	v := g.view()
	if !strings.Contains(v, "▸ ○ d") || !strings.Contains(v, "╰") || !strings.Contains(v, "builder asks about slice c") {
		t.Errorf("the last slice, the newest activity line and the panel's edge are not all in view:\n%s", v)
	}
}

func TestTheReviewScreenNamesWhatARoundSentBack(t *testing.T) {
	t.Parallel()
	status := strings.Join([]string{"ticket: JIG-6", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"a", "green", "1", "-", "-", "Do a"},
			{"fix-1", "queued", "0", "-", "-", "Fix these gate findings:\n\nalpha/a.go:3 Unchecked error\nThe error is dropped."},
		}), "questions: none"}, "\n") + "\n"
	g := newRig(t, "JIG-6")
	g.r.answer("status JIG-6 --store /s", status, 0)
	g.feed(
		jl(at(30, 0), "JIG-6", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(30, 10), "JIG-6", "a", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		jl(at(31, 0), "JIG-6", "", journal.EventGateOpen),
		jl(at(31, 30), "JIG-6", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = 1, "s-reviewer" }),
		jl(at(31, 31), "JIG-6", "fix-1", journal.EventFixSlice, func(l *journal.Line) { l.Attempt = 1 }),
	)
	g.press("down", "enter") // from the build, the current step, to the review
	v := g.view()
	for _, want := range []string{"sent back to the build", "fix-1  round 1  alpha/a.go:3 Unchecked error", "the review sent back fix-1: alpha/a.go:3 Unchecked error"} {
		if !strings.Contains(v, want) {
			t.Errorf("the review screen lacks %q:\n%s", want, v)
		}
	}
	// The goal's other lines never reach a row: every screen keeps its height.
	for _, keys := range [][]string{{"esc"}, {"up", "enter"}} {
		g.press(keys...)
		v := g.view()
		if strings.Contains(v, "The error is dropped.") || strings.Count(v, "\n")+1 != 24 {
			t.Errorf("after %v a fix slice's goal spills over its row:\n%s", keys, v)
		}
	}
}

func TestGoalLineIsOneLineOfAGoal(t *testing.T) {
	t.Parallel()
	for goal, want := range map[string]string{
		"Gate finding r1-f1, kept by the human:\n\nalpha/a.go:3 Unchecked error\nmore": "alpha/a.go:3 Unchecked error",
		"Two\nlines": "Two",
		"Fix these gate findings:\n\nalpha/a.go:3 Unchecked error\nmore": "alpha/a.go:3 Unchecked error",
		"Plain goal": "Plain goal",
		"":           "",
	} {
		if got := goalLine(goal); got != want {
			t.Errorf("goalLine(%q) = %q, want %q", goal, got, want)
		}
	}
}

// TestAReviewerTakingTheRoundShowsOnTheReviewStep: a round the gate opened
// waits for a reviewer until one takes it with `jig review context`, whose
// gate-reviewer line names the session; the activity says both in words.
func TestAReviewerTakingTheRoundShowsOnTheReviewStep(t *testing.T) {
	t.Parallel()
	status := strings.Join([]string{"ticket: JIG-8", "state: green",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"a", "green", "1", "-", "-", "Do a"},
		}), "questions: none"}, "\n") + "\n"
	g := newRig(t, "JIG-8")
	g.r.answer("status JIG-8 --store /s", status, 0)
	open := jl(at(40, 0), "JIG-8", "", journal.EventGateOpen, func(l *journal.Line) { l.Attempt = 1 })
	g.feed(
		jl(at(39, 0), "JIG-8", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(39, 10), "JIG-8", "a", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		open,
	)
	tk := g.m.w.tickets["JIG-8"]
	if r := g.m.steps(tk)[stepReview]; r.State != StateReviewing || r.Text != "round 1 open · waiting for a reviewer" {
		t.Errorf("review before a reviewer = %+v, want round 1 waiting for one", r)
	}
	took := jl(at(40, 5), "JIG-8", "", journal.EventGateReviewer, func(l *journal.Line) { l.Attempt, l.Session = 1, "s-rev" })
	g.feed(took)
	tk = g.m.w.tickets["JIG-8"]
	if r := g.m.steps(tk)[stepReview]; r.State != StateReviewing || r.Text != "round 1 open · reviewer is reading the change" {
		t.Errorf("review once taken = %+v, want the reviewer reading round 1", r)
	}
	if got := g.m.say(tk, open); got != "review round 1 opened" {
		t.Errorf("gate-open says %q", got)
	}
	if got := g.m.say(tk, took); got != "reviewer took round 1" {
		t.Errorf("gate-reviewer says %q", got)
	}
	if stepOfLine(took) != stepReview {
		t.Errorf("a gate-reviewer line is in step %d, want the review", stepOfLine(took))
	}
}
