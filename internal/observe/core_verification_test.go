package observe

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/store"
)

// recordStart records where the ticket's branch started, as a claim does.
func (r *rig) recordStart() {
	r.t.Helper()
	start, err := gitx.RevParse(r.fx.RepoDir, "HEAD")
	if err != nil {
		r.t.Fatal(err)
	}
	if err := r.st.WriteStartSHA(r.fx.Ticket, "fixture-repo", start); err != nil {
		r.t.Fatal(err)
	}
}

func TestStopHoldsOnACommitWithNoRecordedResult(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.unattendedClaim("a")
	r.recordStart()

	// A commit made while no `jig verify` ran has no recorded result.
	sha := r.commit("a", "a.txt")
	short := sha[:12]
	first := r.handle(r.ev(KindStop))
	if !strings.Contains(first.Block, "commit "+short+" for slice a has no recorded result") ||
		!strings.Contains(first.Block, "jig verify "+r.fx.Ticket+" --slice a") {
		t.Fatalf("Block = %q, want the commit held with the command that records a result", first.Block)
	}
	if strings.Contains(first.Block, "stale") {
		t.Fatalf("Block = %q, want no stale verification", first.Block)
	}
}

// TestStopSaysNothingOfAnOldPendingLine: an old worker's pending line and its
// spawned line (a pid that is long gone) are no result and no work in flight:
// the commit reads as having no recorded result, said once as it was, and
// neither line is told as a verification stale or running.
func TestStopSaysNothingOfAnOldPendingLine(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.unattendedClaim("a")
	r.recordStart()

	sha := r.commit("a", "a.txt")
	r.add(journal.Line{Event: journal.EventVerifyPending, Slice: "a", Commit: sha, Attempt: 1})
	if err := ledger.Append(r.d.JigHome, journal.Line{Ticket: r.fx.Ticket, Slice: "a", Event: journal.EventVerifySpawned, Commit: sha, PID: 1 << 30}); err != nil {
		t.Fatal(err)
	}
	res := r.handle(r.ev(KindStop))
	if !strings.Contains(res.Block, "commit "+sha[:12]+" for slice a has no recorded result") {
		t.Fatalf("Block = %q, want the commit with no recorded result held", res.Block)
	}
	for _, bad := range []string{"stale", "running", "worker", "pending"} {
		if strings.Contains(res.Block, bad) {
			t.Fatalf("Block = %q, tells an old line as %q", res.Block, bad)
		}
	}

	// A pass of the commit settles it, whatever the old lines say.
	r.add(journal.Line{Event: journal.EventOracle, Outcome: "pass", Slice: "a", Commit: sha, Attempt: 2})
	r.add(journal.Line{Event: journal.EventVerified, Slice: "a", Commit: sha, Attempt: 2})
	if again := r.handle(r.ev(KindStop)); strings.Contains(again.Block, sha[:12]) {
		t.Fatalf("Block = %q, want a passed commit left alone", again.Block)
	}
}

func TestStopTellsFailedWithoutHolding(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.unattendedClaim("a")
	r.recordStart()

	for _, s := range []string{"a", "b", "c"} {
		if err := r.st.WriteSliceState(r.fx.Ticket, s, store.SliceState{State: "building", Attempts: 1}); err != nil {
			t.Fatal(err)
		}
	}
	failed := r.commit("a", "a.txt")
	stopped := r.commit("b", "b.txt")
	errored := r.commit("c", "c.txt")
	r.add(journal.Line{Event: journal.EventOracle, Outcome: "fail", Slice: "a", Commit: failed, Attempt: 1, Summary: "ok pkg 0.1s --- FAIL: TestX (0.00s) boom"})
	r.add(journal.Line{Event: journal.EventOracle, Outcome: "fail", Cause: journal.CauseStopped, Slice: "b", Commit: stopped, Attempt: 1})
	r.add(journal.Line{Event: journal.EventVerifyError, Outcome: "error", Slice: "c", Commit: errored, Attempt: 1, Summary: "no worktree"})

	// The session's own notes hold it once for a failed run, as they always
	// did; past that, the verifications themselves hold nobody back.
	r.handle(r.ev(KindStop))
	res := r.handle(r.ev(KindStop))
	if res.Block != "" {
		t.Fatalf("Block = %q, want an unattended builder to be free to stop on a failed, stopped or could-not-run verification", res.Block)
	}
	if !strings.Contains(res.Message, failed[:12]+" for slice a failed: --- FAIL: TestX") ||
		!strings.Contains(res.Message, stopped[:12]+" for slice b stopped, not counted") ||
		!strings.Contains(res.Message, errored[:12]+" for slice c could not run: no worktree") {
		t.Errorf("Message = %q, want all three told", res.Message)
	}
}
