package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
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

	// The ticket's last run, after the review, is a sub-step of the build of
	// its own: the run jig recorded for the reviewed head, in the seconds it
	// recorded, whatever the harness recorded around it.
	f := newRig(t, "JIG-2")
	atClock(f, "2026-10-23T09:30:00Z")
	f.r.answer("status JIG-2 --store /s", statusJIG2Done(), 0)
	f.feed(jig2()...)
	f.feed(
		jl(at(13, 35), "JIG-2", "", journal.EventFinalOracle, with("", sha('d'), "pass", "", "", ""), func(l *journal.Line) { l.Seconds = 240 }),
		toolOn("JIG-2", at(13, 36), "s-main", "jig verify JIG-2 --final", said("Run the final check")),
		jl(at(13, 37), "JIG-2", "", journal.EventEnvDown),
	)
	tk := f.m.w.tickets["JIG-2"]
	d := f.m.phaseOf(tk)
	c := f.m.clocks(tk, d)
	var sum time.Duration
	for _, s := range d.Of(phase.Build).Subs {
		sum += c.work.of(stepBuild, s.Key)
	}
	if c.work.of(stepBuild, "final") != 4*time.Minute || c.work.of(stepBuild, "") != 0 || sum != f.m.steps(tk)[stepBuild].took {
		t.Errorf("the build: final run %v, the step's own time %v, sub-steps add up to %v, row says %v", c.work.of(stepBuild, "final"), c.work.of(stepBuild, ""), sum, f.m.steps(tk)[stepBuild].took)
	}
}

// TestTheFinalRunReadsTheRunAtTheReviewedHead: the build's final run is the one
// jig recorded for the head the latest round reviewed (ddd...): its word is that
// run's outcome and its time the seconds it recorded; a run at another commit or
// one that covers nothing is none.
func TestTheFinalRunReadsTheRunAtTheReviewedHead(t *testing.T) {
	t.Parallel()
	run := func(ts string, commit byte, outcome string, secs int, mod ...func(*journal.Line)) events.Envelope {
		return jl(ts, "JIG-2", "", journal.EventFinalOracle, append([]func(*journal.Line){with("", sha(commit), outcome, "", "", ""), func(l *journal.Line) { l.Seconds = secs }}, mod...)...)
	}
	read := func(lines ...events.Envelope) (phase.Sub, time.Duration) {
		g := newRig(t, "JIG-2")
		atClock(g, "2026-10-23T09:30:00Z")
		g.r.answer("status JIG-2 --store /s", statusJIG2Done(), 0)
		g.feed(jig2()...)
		g.feed(lines...)
		if len(lines) > 0 {
			g.feed(jl(at(13, 36), "JIG-2", "", journal.EventEnvDown)) // closes into the sub-step before it
		}
		tk := g.m.w.tickets["JIG-2"]
		d := g.m.phaseOf(tk)
		subs := d.Of(phase.Build).Subs
		return subs[len(subs)-1], g.m.clocks(tk, d).work.of(stepBuild, "final")
	}
	none := "none at the reviewed head " + strings.Repeat("d", 7)
	for name, c := range map[string]struct {
		lines  []events.Envelope
		state  string
		detail string
		took   time.Duration
	}{
		"no run":                    {nil, phase.SubPending, none, 0},
		"a run at an older head":    {[]events.Envelope{run(at(13, 35), 'c', "pass", 240)}, phase.SubPending, none, 0},
		"a run that covers nothing": {[]events.Envelope{run(at(13, 35), 'd', "pass", 240, func(l *journal.Line) { l.Uncovered = "dirty" })}, phase.SubPending, none, 0},
		"a failure then a pass":     {[]events.Envelope{run(at(13, 35), 'd', "fail", 60), run(at(13, 40), 'd', "pass", 240)}, phase.SubDone, "passed", 4 * time.Minute},
		"a pass then a failure":     {[]events.Envelope{run(at(13, 35), 'd', "pass", 240), run(at(13, 50), 'd', "fail", 90)}, phase.SubDone, "failed", 90 * time.Second},
	} {
		sub, took := read(c.lines...)
		if sub.Key != "final" || sub.State != c.state || sub.Detail != c.detail || took != c.took {
			t.Errorf("%s: the final run reads %+v and %v, want %s %q and %v", name, sub, took, c.state, c.detail, c.took)
		}
	}
}

// TestTheFinalRunOfAProjectOfSeveralRepos: the screens read the final run as
// publish does, by the same function. A ticket that builds in one repo of a
// project of several has a bare head in its status and a repo on its final-run
// line; a ticket built in two reads the run of each, and a failure in one is
// not hidden by a pass in the other.
func TestTheFinalRunOfAProjectOfSeveralRepos(t *testing.T) {
	t.Parallel()
	finalIn := func(ts, repo string, commit byte, outcome string, secs int) events.Envelope {
		return jl(ts, "JIG-2", "", journal.EventFinalOracle, with("", sha(commit), outcome, "", "", ""), func(l *journal.Line) { l.Repo, l.Seconds = repo, secs })
	}
	read := func(status string, lines []events.Envelope) (phase.Sub, time.Duration) {
		g := newRig(t, "JIG-2")
		atClock(g, "2026-10-23T09:30:00Z")
		g.r.answer("status JIG-2 --store /s", status, 0)
		g.feed(lines...)
		tk := g.m.w.tickets["JIG-2"]
		d := g.m.phaseOf(tk)
		subs := d.Of(phase.Build).Subs
		return subs[len(subs)-1], g.m.clocks(tk, d).work.of(stepBuild, "final")
	}

	// One repo of several: a bare head, a run that names its repo.
	sub, took := read(statusJIG2Done(), append(jig2(), finalIn(at(13, 35), "api", 'd', "pass", 240)))
	if sub.Key != "final" || sub.State != phase.SubDone || sub.Detail != "passed" || took != 4*time.Minute {
		t.Errorf("a ticket built in one repo of several reads %+v and %v, want its run passed in 4m", sub, took)
	}

	// Two repos: api's head ccc..., web's ddd...
	lines := append(jig2Repos(), finalIn(at(13, 40), "api", 'c', "fail", 30), finalIn(at(13, 45), "web", 'd', "pass", 90))
	sub, took = read(statusJIG2Repos(), lines)
	if sub.State != phase.SubDone || sub.Detail != "failed" || took != 2*time.Minute {
		t.Errorf("a ticket built in two repos reads %+v and %v, want failed in 2m (30s and 90s)", sub, took)
	}
	sub, _ = read(statusJIG2Repos(), append(jig2Repos(), finalIn(at(13, 45), "web", 'd', "pass", 90)))
	if sub.State != phase.SubPending || !strings.Contains(sub.Detail, "none at the reviewed head ccccccc") {
		t.Errorf("a ticket whose api head has no run reads %+v, want none at ccccccc", sub)
	}
}

// TestTheAsksWaitIsNeverReviewWork: the asks a round left wait for the person
// from the round's close to the decision of the last of them, and nothing in it
// is work: not a call of a session in between, not the time before one of
// several is decided, not a run of decisions that ends the review's lines.
func TestTheAsksWaitIsNeverReviewWork(t *testing.T) {
	t.Parallel()
	read := func(status string, lines []events.Envelope, now string) (*rig, waitSpan) {
		g := newRig(t, "JIG-2")
		atClock(g, now)
		g.r.answer("status JIG-2 --store /s", status, 0)
		g.feed(lines...)
		tk := g.m.w.tickets["JIG-2"]
		w, _ := g.m.clocks(tk, g.m.phaseOf(tk)).waitOf(stepReview, "asks")
		return g, w
	}
	review := func(g *rig) string { return g.m.steps(g.m.w.tickets["JIG-2"])[stepReview].Took }

	// (a) a session calls before the first decision.
	lines := append(append([]events.Envelope{}, jig2()...),
		toolOn("JIG-2", at(11, 32), "s-main", "jig next JIG-2"),
		toolOn("JIG-2", at(11, 59), "s-main", "jig triage JIG-2"))
	g, w := read(statusJIG2Done(), lines, "2026-10-23T09:20:00Z")
	if got := review(g); got != "1m 55s" || w.end.Sub(w.start) != 35*time.Second {
		t.Errorf("(a) the review took %q, its asks waited %v, want 1m 55s and 35s", got, w.end.Sub(w.start))
	}

	// (b) one ask decided and one still open: the open one still waits.
	g, _ = read(statusJIG2(), jig2()[:5], "2026-10-23T09:12:30Z")
	if got := review(g); got != "30s" {
		t.Errorf("(b) the review took %q with an ask still open, want 30s", got)
	}

	// (c) the decisions are the review's latest lines.
	g, w = read(statusJIG2Done(), jig2()[:6], "2026-10-23T09:12:30Z")
	if got := review(g); got != "30s" || w.end.Sub(w.start) != 35*time.Second {
		t.Errorf("(c) the review took %q, its asks waited %v, want 30s and 35s", got, w.end.Sub(w.start))
	}
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
