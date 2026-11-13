package verifydeliver

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// recordFail journals a failed oracle line of the run that ended for cause
// ("" for a test failure).
func recordFail(t *testing.T, d Deps, ticket, commit, command, cause string) {
	t.Helper()
	line := journal.Line{Slice: "a", Event: "oracle", Outcome: "fail", Commit: commit, Attempt: 1, Command: command, Env: "rig", Cause: cause}
	if err := journal.Append(d.Store, ticket, line); err != nil {
		t.Fatal(err)
	}
}

// TestGateReusesAPassBesideAnUncountedFail: a pass followed by a timed-out or a
// stopped run on the same tree is still reused and nothing is run, and so is a
// light pass followed by such a run.
func TestGateReusesAPassBesideAnUncountedFail(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	alpha, beta := func() string { return testCmd(t, fx, "alpha") }, func() string { return testCmd(t, fx, "beta") }

	step(t, "a pass then a timeout and a stop on the same tree", func(t *testing.T) {
		rec := &oracleRecorder{}
		d.Oracle = rec.run
		head := commitInBuildLease(t, fx, map[string]string{"alpha/note.txt": "same tree\n"})
		recordOracle(t, d, fx.Ticket, "pass", head, alpha(), "rig")
		recordOracle(t, d, fx.Ticket, "pass", head, beta(), "rig")
		recordFail(t, d, fx.Ticket, head, alpha(), journal.CauseTimeout)
		recordFail(t, d, fx.Ticket, head, beta(), journal.CauseStopped)

		got := reusedByWorkspace(gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed)
		if got["alpha"] != head || got["beta"] != head {
			t.Errorf("reused_from by workspace = %v, want both reused from %s", got, head)
		}
		if ran := rec.ran(); len(ran) != 0 {
			t.Errorf("the gate ran %q, want nothing run", ran)
		}
	})

	step(t, "a light pass then an uncounted fail since", func(t *testing.T) {
		rec := &oracleRecorder{}
		d.Oracle = rec.run
		base := declareLight(t, fx)
		recordOracle(t, d, fx.Ticket, "pass", base, alpha(), "rig")
		recordOracle(t, d, fx.Ticket, "pass", base, beta(), "rig")
		other := commitInBuildLease(t, fx, map[string]string{"README.md": "between\n"})
		recordFail(t, d, fx.Ticket, other, alpha(), journal.CauseTimeout)
		recordFail(t, d, fx.Ticket, other, beta(), journal.CauseStopped)
		commitInBuildLease(t, fx, map[string]string{"README.md": "after\n"})

		got := runsByWorkspace(gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed)
		for _, ws := range []string{"alpha", "beta"} {
			if got[ws].ReusedFrom != base || got[ws].Ran != lightGateOracle {
				t.Errorf("%s = %+v, want reused from %s with the light oracle", ws, got[ws], base)
			}
		}
		if ran := rec.ran(); len(ran) != 1 || ran[0] != lightGateOracle {
			t.Errorf("the gate ran %q, want the light oracle alone", ran)
		}
	})
}

// TestGateRerunsAfterATestFailure: a pass followed by a fail with no cause on
// the same tree is not reused; the gate runs the oracle again.
func TestGateRerunsAfterATestFailure(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	rec := &oracleRecorder{}
	d.Oracle = rec.run
	alpha := testCmd(t, fx, "alpha")
	head := commitInBuildLease(t, fx, map[string]string{"alpha/note.txt": "flaky\n"})
	recordOracle(t, d, fx.Ticket, "pass", head, alpha, "rig")
	recordFail(t, d, fx.Ticket, head, alpha, "")

	if got := reusedByWorkspace(gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed); got["alpha"] != "" {
		t.Errorf("test@alpha reused from %s, want it run", got["alpha"])
	}
	found := false
	for _, c := range rec.ran() {
		found = found || c == alpha
	}
	if !found {
		t.Errorf("the gate ran %q, want test@alpha among them", rec.ran())
	}
}
