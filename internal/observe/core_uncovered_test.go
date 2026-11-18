package observe

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestAnUncoveredFailureIsNoEnforcedNote: a failed run that covers no commit (a
// dirty tree, another command) is no verification of the commit it names, so
// it is no "verification FAILED" note and holds no unattended Stop.
func TestAnUncoveredFailureIsNoEnforcedNote(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.unattendedClaim("b")
	r.recordStart()
	sha := r.commit("b", "b.txt")
	r.add(journal.Line{Event: journal.EventOracle, Slice: "b", Commit: sha, Attempt: 1, Outcome: "fail", Summary: "FAIL beta TestX", Uncovered: journal.UncoveredDirtyBefore})

	res := r.handle(r.ev(KindTool, func(e *Event) { e.Tool, e.Command = "Bash", "ls" }))
	if strings.Contains(res.Context, "FAILED") {
		t.Errorf("tool context = %q, want no failure told for a run that covers nothing", res.Context)
	}
	stop := r.handle(r.ev(KindStop))
	if strings.Contains(stop.Block, "FAILED") || strings.Contains(stop.Message, "failed") {
		t.Errorf("Stop = %+v, want no failure named for a run that covers nothing", stop)
	}
}

// TestStopHoldsOnlyOnASlicesLatestCommit: a slice committed twice before its
// run (a test commit, then the code) has one commit that can be cleared by
// `jig verify`, which runs at HEAD. Only that commit is held to a result; the
// older one is history.
func TestStopHoldsOnlyOnASlicesLatestCommit(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.unattendedClaim("a")
	r.recordStart()
	first := r.commit("a", "a1.txt")
	second := r.commit("a", "a2.txt")

	res := r.handle(r.ev(KindStop))
	if !strings.Contains(res.Block, "commit "+second[:12]+" for slice a is not verified") {
		t.Fatalf("Block = %q, want the latest commit held to a result", res.Block)
	}
	if strings.Contains(res.Block, first[:12]) {
		t.Errorf("Block = %q, want the older commit of the slice left out", res.Block)
	}
}
