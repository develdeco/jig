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

// TestStopHoldsOnlyOnASlicesLatestCommitUnlessVerifiedAtADescendant: slice a committed, then
// slice b, then both verified at HEAD (the plain order of a multi-slice
// ticket): slice a's commit is history, for `jig verify` at HEAD records a
// verification of HEAD and could never clear it.
func TestStopHoldsOnlyOnASlicesLatestCommitUnlessVerifiedAtADescendant(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.unattendedClaim("a")
	r.recordStart()
	a1 := r.commit("a", "a1.txt")
	b1 := r.commit("b", "b1.txt")
	for _, slice := range []string{"a", "b"} {
		r.add(journal.Line{Event: journal.EventOracle, Slice: slice, Commit: b1, Attempt: 1, Outcome: "pass"})
		r.add(journal.Line{Event: journal.EventVerified, Slice: slice, Commit: b1, Attempt: 1})
	}

	res := r.handle(r.ev(KindStop))
	if strings.Contains(res.Block, a1[:12]) || strings.Contains(res.Message, a1[:12]) {
		t.Errorf("Stop = %+v, want no hold naming slice a's commit: it is verified at a descendant", res)
	}
}
