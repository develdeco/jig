package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

func TestModelNameIsHowPeopleCallTheModel(t *testing.T) {
	t.Parallel()
	for id, want := range map[string]string{
		"claude-sonnet-5":           "Sonnet 5",
		"claude-opus-5-5":           "Opus 5.5",
		"claude-opus-5-5[1m]":       "Opus 5.5",
		"claude-haiku-4-5-2025100":  "Haiku 4.5.2025100",
		"claude-haiku-4-5-20251001": "Haiku 4.5",
		"sonnet":                    "Sonnet",
		"gpt-5":                     "Gpt 5",
		"":                          "",
	} {
		if got := modelName(id); got != want {
			t.Errorf("modelName(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestPersonNameDropsTheEmail(t *testing.T) {
	t.Parallel()
	if got := personName("Ada Lovelace <ada@example.invalid>"); got != "Ada Lovelace" {
		t.Errorf("personName = %q", got)
	}
	if got := personName("Ada"); got != "Ada" {
		t.Errorf("personName = %q", got)
	}
}

// reviewRound is a round of SHOP-1 that session reviews on model: opened, then
// taken, in the lines of the gate; and the ledger's word on the session, which
// started on startModel.
func reviewRound(session, startModel, model string) []events.Envelope {
	return []events.Envelope{
		ll(at(0, 0), "SHOP-1", journal.EventSessionStart, func(l *journal.Line) { l.Session, l.Harness, l.Model = session, "claude", startModel }),
		jl(at(20, 0), "SHOP-1", "", journal.EventGateOpen),
		jl(at(20, 10), "SHOP-1", "", journal.EventGateReviewer, func(l *journal.Line) { l.Session, l.Attempt, l.Model = session, 1, model }),
	}
}

// TestAReviewerSubagentIsNamedAsOneAndByTheModelItRanOn: the subagent that
// reviews shares its parent's session id, so it is named for what it does, not
// for the first role the id played ("planner"), and the model is the one the
// round names, not the one the parent started on.
func TestAReviewerSubagentIsNamedAsOneAndByTheModelItRanOn(t *testing.T) {
	t.Parallel()
	g := newRig(t, "SHOP-1")
	g.r.answer("status SHOP-1 --store /s", statusSHOP1(), 0)
	// s-builder claimed slices and is the session whose subagent reviews.
	g.feed(shop1()...)
	g.feed(reviewRound("s-builder", "claude-sonnet-5", "claude-opus-5")...)

	// The review sub-steps, with the cursor on the review.
	g.press("down")
	v := g.view()
	for _, want := range []string{"3 Review · sub-steps", "round 1 · taken by reviewer subagent (Claude Code · Opus 5)", "round 1 · reviewer subagent (Claude Code · Opus 5) took it"} {
		if !strings.Contains(v, want) {
			t.Errorf("the review screen lacks %q:\n%s", want, v)
		}
	}
	for _, not := range []string{"planner", "Sonnet", "builder (", "builder 1 ("} {
		if strings.Contains(v, not) {
			t.Errorf("the review screen has %q for the reviewer subagent:\n%s", not, v)
		}
	}

	// The lines that close the round carry no model of their own here: the
	// round's is the one its gate-reviewer line gave.
	g.feed(jl(at(21, 0), "SHOP-1", "", journal.EventGateClean, func(l *journal.Line) { l.Session, l.Attempt = "s-builder", 1 }))
	tk := g.m.w.tickets["SHOP-1"]
	clean := tk.lines[len(tk.lines)-1]
	if got := g.m.say(tk, clean); got != "reviewer subagent (Claude Code · Opus 5) finished: clean" {
		t.Errorf("gate-clean says %q, want the subagent and the model of the round", got)
	}
	if got := g.m.say(tk, jl(at(21, 5), "SHOP-1", "", journal.EventGateRound, func(l *journal.Line) { l.Session, l.Attempt, l.Model = "s-builder", 1, "claude-haiku-4-5" })); got != "reviewer subagent (Claude Code · Haiku 4.5) finished with findings" {
		t.Errorf("gate-round says %q, want the model its own line names", got)
	}
}

// TestAReviewerThatBuiltNothingIsJustTheReviewer: a session that played no other
// role on the ticket is the reviewer, and the model is the one it ran on.
func TestAReviewerThatBuiltNothingIsJustTheReviewer(t *testing.T) {
	t.Parallel()
	g := newRig(t, "SHOP-1")
	g.r.answer("status SHOP-1 --store /s", statusSHOP1(), 0)
	g.feed(shop1()...)
	g.feed(reviewRound("s-fresh", "claude-opus-5", "")...)

	tk := g.m.w.tickets["SHOP-1"]
	took := tk.lines[len(tk.lines)-1]
	if got := g.m.say(tk, took); got != "reviewer (Claude Code · Opus 5) took it" {
		t.Errorf("gate-reviewer says %q, want the reviewer on the model it started on", got)
	}
	if got := tk.reviewerName("s-fresh"); got != "reviewer" {
		t.Errorf("reviewerName = %q, want reviewer", got)
	}
	if got := tk.reviewerName("s-builder"); got != "reviewer subagent" {
		t.Errorf("reviewerName of the builder = %q, want reviewer subagent", got)
	}
	if got := tk.reviewerName("s-nobody"); got != "" {
		t.Errorf("reviewerName of a session with no role = %q, want none", got)
	}
}
