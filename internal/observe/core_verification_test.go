package observe

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
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

// exitedPID is the pid of a process that ran and ended: a worker that died.
func exitedPID(t *testing.T) int {
	t.Helper()
	c := exec.Command(os.Args[0], "-test.run=^$")
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	return c.Process.Pid
}

func TestStopHoldsOnNotVerifiedAndStale(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.unattendedClaim("a")
	r.recordStart()

	// A commit made while no jig ran has no verification at all.
	sha := r.commit("a", "a.txt")
	short := sha[:12]
	first := r.handle(r.ev(KindStop))
	if !strings.Contains(first.Block, "commit "+short+" for slice a is not verified") ||
		!strings.Contains(first.Block, "jig verify "+r.fx.Ticket+" "+short+" --slice a --wait") {
		t.Fatalf("Block = %q, want the unverified commit held with the command that verifies it", first.Block)
	}

	// A pending line whose worker died is stale.
	r.add(journal.Line{Event: journal.EventVerifyPending, Slice: "a", Commit: sha, Attempt: 1})
	if err := ledger.Append(r.d.JigHome, journal.Line{Ticket: r.fx.Ticket, Slice: "a", Event: journal.EventVerifySpawned, Commit: sha, PID: exitedPID(t)}); err != nil {
		t.Fatal(err)
	}
	second := r.handle(r.ev(KindStop))
	if !strings.Contains(second.Block, "verification of "+short+" for slice a is stale") || strings.Contains(second.Block, "still running") {
		t.Fatalf("Block = %q, want the stale verification held", second.Block)
	}
}

func TestStopTellsFailedAndStoppedWithoutHolding(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.unattendedClaim("a")
	r.recordStart()

	failed := r.commit("a", "a.txt")
	stopped := r.commit("b", "b.txt")
	r.add(journal.Line{Event: journal.EventOracle, Outcome: "fail", Slice: "a", Commit: failed, Attempt: 1, Summary: "ok pkg 0.1s --- FAIL: TestX (0.00s) boom"})
	r.add(journal.Line{Event: journal.EventOracle, Outcome: "fail", Cause: journal.CauseStopped, Slice: "b", Commit: stopped, Attempt: 1})

	// The session's own notes hold it once for a failed run, as they always
	// did; past that, the verifications themselves hold nobody back.
	r.handle(r.ev(KindStop))
	res := r.handle(r.ev(KindStop))
	if res.Block != "" {
		t.Fatalf("Block = %q, want an unattended builder to be free to stop on a failed or stopped verification", res.Block)
	}
	if !strings.Contains(res.Message, failed[:12]+" for slice a failed: --- FAIL: TestX") ||
		!strings.Contains(res.Message, stopped[:12]+" for slice b stopped, not counted") {
		t.Errorf("Message = %q, want both told", res.Message)
	}
}
