package observe

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestEachAgentOfASessionIsHeldByItsOwnClaim: two agents of one session claim
// different slices of one ticket unattended, at the same moment (a ticket
// subagent and a repo builder nested in it). Each is held at its own stop and
// the main session is not; the claim of the second does not take the hold
// from the first, and the release of one slice frees only its holder.
func TestEachAgentOfASessionIsHeldByItsOwnClaim(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.claimBy("a-7", "a", true)
	r.claimBy("a-9", "b", true)
	r.pending("a", "a.txt")

	stop := func(agent string) Result {
		return r.handle(r.ev(KindStop, func(e *Event) { e.Agent = agent }))
	}
	for _, agent := range []string{"a-7", "a-9"} {
		if res := stop(agent); !strings.Contains(res.Block, "this agent's claim is unattended") {
			t.Errorf("Stop of %s = %+v, want it held by its own claim", agent, res)
		}
	}
	if res := stop(""); res.Block != "" {
		t.Errorf("main Stop = %+v, want no block: the claims are the subagents'", res)
	}

	r.add(journal.Line{Event: journal.EventRelease, Slice: "a", Session: "s1"})
	r.pending("b", "b.txt")
	if res := stop("a-7"); res.Block != "" {
		t.Errorf("Stop of a-7 after releasing its slice = %+v, want no block", res)
	}
	if res := stop("a-9"); !strings.Contains(res.Block, "this agent's claim is unattended") {
		t.Errorf("Stop of a-9 after a-7 released its slice = %+v, want it still held", res)
	}
}

// TestAnAgentIsHeldByItsLatestClaimNotAnEarlierOne: an agent whose latest
// unreleased claim is interactive is not held by an earlier unattended one,
// and one that releases its latest claim is held again by the earlier.
func TestAnAgentIsHeldByItsLatestClaimNotAnEarlierOne(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.claimBy("a-7", "a", true)
	r.claimBy("a-9", "b", true)
	r.claimBy("a-7", "c", false)
	r.pending("a", "a.txt")

	if res := r.handle(r.ev(KindStop, subagent)); res.Block != "" {
		t.Errorf("Stop of a-7 with an interactive latest claim = %+v, want no block", res)
	}
	r.add(journal.Line{Event: journal.EventRelease, Slice: "c", Session: "s1"})
	if res := r.handle(r.ev(KindStop, subagent)); !strings.Contains(res.Block, "this agent's claim is unattended") {
		t.Errorf("Stop of a-7 after releasing the interactive claim = %+v, want the earlier unattended claim to hold it", res)
	}
}

// TestAnUnknownHolderOfTheLatestClaimHoldsEveryAgent: when the latest claim
// has no tool line (a harness without hooks), the session is the holder, as
// before the hold was per agent, whoever claimed before.
func TestAnUnknownHolderOfTheLatestClaimHoldsEveryAgent(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.claimBy("a-7", "a", true)
	r.unattendedClaim("b")
	r.pending("a", "a.txt")

	for _, agent := range []string{"", "a-7", "a-9"} {
		if res := r.handle(r.ev(KindStop, func(e *Event) { e.Agent = agent })); res.Block == "" {
			t.Errorf("Stop of agent %q with the latest claim's holder unknown = %+v, want the session held", agent, res)
		}
	}
}
