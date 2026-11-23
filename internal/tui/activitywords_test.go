package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestActivityEnvUnavailablePolicy: the line names the class's policy from the
// line's outcome and adds the summary only when there is one, never a colon
// with nothing after it.
func TestActivityEnvUnavailablePolicy(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-6")
	tk := ticketOf("JIG-6")
	for _, tc := range []struct{ outcome, summary, want string }{
		{"defer-ci", "", "its environment is unavailable (policy: defer-ci)"},
		{"pause", "", "its environment is unavailable (policy: pause)"},
		{"pause", "no docker", "its environment is unavailable (policy: pause): no docker"},
		{"defer-ci", "no docker", "its environment is unavailable (policy: defer-ci): no docker"},
	} {
		l := jl(at(1, 0), "JIG-6", "a", journal.EventEnvUnavailable, with("", "", tc.outcome, tc.summary, "", ""))
		if got := g.m.say(tk, l); got != tc.want {
			t.Errorf("outcome %q summary %q says %q, want %q", tc.outcome, tc.summary, got, tc.want)
		}
	}
}

// TestActivityApprovalByAgent: an agent's approval names the role of its
// session and says it is an agent, and leaves the recorded git identity out,
// since that is not who approved; a person's approval is the person.
func TestActivityApprovalByAgent(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-6")
	agent := jl(at(2, 0), "JIG-6", "", journal.EventApproval, with("s-planner", "", "", "Ada Lovelace <a@example.invalid>", "", "agent"))
	anon := jl(at(2, 1), "JIG-6", "", journal.EventApproval, with("", "", "", "Ada Lovelace <a@example.invalid>", "", "agent"))
	person := jl(at(2, 2), "JIG-6", "", journal.EventApproval, with("s-planner", "", "", "Ada Lovelace <a@example.invalid>", "", "person"))
	tk := ticketOf("JIG-6", agent, anon, person)
	for _, tc := range []struct {
		name string
		l    int
		want string
	}{{"an agent in a session", 0, "by the planner (an agent)"}, {"an agent with no session", 1, "by an agent"}, {"a person", 2, "by Ada Lovelace"}} {
		if got := g.m.say(tk, tk.lines[tc.l]); got != tc.want {
			t.Errorf("%s: says %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestReviewerOwnChange: a round the status says was not independent names its
// reviewer "builder reviewing its own change", in the activity and for the
// sub-steps; an independent, unknown or unrecorded round keeps "reviewer
// subagent" for a session that also built.
func TestReviewerOwnChange(t *testing.T) {
	t.Parallel()
	for independent, want := range map[string]string{
		"false":   "builder reviewing its own change (Claude Code · Opus 5) took it",
		"true":    "reviewer subagent (Claude Code · Opus 5) took it",
		"unknown": "reviewer subagent (Claude Code · Opus 5) took it",
		"":        "reviewer subagent (Claude Code · Opus 5) took it",
	} {
		g := newRig(t, "SHOP-1")
		g.r.answer("status SHOP-1 --store /s", statusSHOP1(), 0)
		g.feed(shop1()...)
		g.feed(reviewRound("s-builder", "claude-sonnet-5", "claude-opus-5")...)
		tk := g.m.w.tickets["SHOP-1"]
		if independent != "" {
			tk.status.Rounds = []statusRound{{N: 1, Independent: independent}}
		} else {
			tk.status.Rounds = nil
		}
		took := tk.lines[len(tk.lines)-1]
		if got := g.m.say(tk, took); got != want {
			t.Errorf("independent %q: the activity says %q, want %q", independent, got, want)
		}
		subWant := strings.SplitN(want, " (", 2)[0]
		if got := g.m.reviewerRole(tk, "s-builder", 1); got != subWant {
			t.Errorf("independent %q: the sub-steps name %q, want %q", independent, got, subWant)
		}
	}
}

// TestWrittenFactsDrawDone: the lines the TUI writes for a brief's sections and
// the slices seen in a status draw the done icon, not the in-progress one.
func TestWrittenFactsDrawDone(t *testing.T) {
	t.Parallel()
	for _, event := range []string{eventBriefSeen, eventSlicesSeen} {
		l := jl(at(1, 0), "JIG-6", "", event)
		if got := eventState(l); got != StateOK {
			t.Errorf("%s is drawn %q, want %q", event, got, StateOK)
		}
	}
}
