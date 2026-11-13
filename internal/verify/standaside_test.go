package verify

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// exitedPID is the pid of a process that ran and ended: a worker that died.
func exitedPID(t *testing.T) int {
	t.Helper()
	c := exec.Command(os.Args[0], "-test.run=^$")
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	return c.Process.Pid
}

// TestStaleVerificationStandsAside: a pending line whose worker is gone is not
// an answer. The request runs the oracle again with no rerun, and the line
// then reads as the run's outcome.
func TestStaleVerificationStandsAside(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	spawnedAt := time.Now().Add(-20 * time.Minute)
	if err := journal.Append(r.st, r.fx.Ticket, journal.Line{Slice: "a", Event: journal.EventVerifyPending, Commit: sha, Attempt: 1, TS: spawnedAt.Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Append(r.deps.JigHome, journal.Line{Ticket: r.fx.Ticket, Slice: "a", Event: journal.EventVerifySpawned, Commit: sha, PID: exitedPID(t), TS: spawnedAt.Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	res, err := Run(r.deps, r.request("a", sha))
	if err != nil || res.Existing || res.State != journal.VerifyPass {
		t.Fatalf("Run = %+v, %v, want the oracle to run again and pass", res, err)
	}
	if len(r.oracle.seen()) != 1 {
		t.Errorf("oracle calls = %d, want 1", len(r.oracle.seen()))
	}
}

// TestStoppedVerificationStandsAside: a run jig was told to stop, and a run
// that could not start its oracle, never judged the code: the request runs it.
func TestStoppedVerificationStandsAside(t *testing.T) {
	t.Parallel()
	cases := map[string]journal.Line{
		"stopped oracle": {Event: journal.EventOracle, Outcome: "fail", Cause: journal.CauseStopped, Summary: "stopped"},
		"verify error":   {Event: journal.EventVerifyError, Outcome: "error", Summary: "no worktree"},
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, fixture.Opts{})
			sha := r.fixClamp()
			line.Slice, line.Commit, line.Attempt = "a", sha, 1
			if err := journal.Append(r.st, r.fx.Ticket, line); err != nil {
				t.Fatal(err)
			}
			res, err := Run(r.deps, r.request("a", sha))
			if err != nil || res.Existing || res.State != journal.VerifyPass {
				t.Fatalf("Run = %+v, %v, want the oracle to run again and pass", res, err)
			}
			if len(r.oracle.seen()) != 1 {
				t.Errorf("oracle calls = %d, want 1", len(r.oracle.seen()))
			}
		})
	}
}

// TestFailedVerificationStillStands: an oracle that failed on the code is an
// answer; only a rerun asks again.
func TestFailedVerificationStillStands(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	if err := journal.Append(r.st, r.fx.Ticket, journal.Line{Slice: "a", Event: journal.EventOracle, Outcome: "fail", Commit: sha, Attempt: 1, Summary: "--- FAIL: TestX"}); err != nil {
		t.Fatal(err)
	}
	res, err := Run(r.deps, r.request("a", sha))
	if err != nil || !res.Existing || res.State != journal.VerifyFail {
		t.Fatalf("Run = %+v, %v, want the failure to stand", res, err)
	}
	if len(r.oracle.seen()) != 0 {
		t.Errorf("a failed record was run over: oracle calls %d", len(r.oracle.seen()))
	}
}

// TestAWorkerSpawnedForAStaleVerificationRunsTheOracle: the worker Spawn starts
// for a stale pending line finds its own spawned line in the ledger, and must
// not read the old line as one a live worker is on.
func TestAWorkerSpawnedForAStaleVerificationRunsTheOracle(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	old := time.Now().Add(-20 * time.Minute).Format(time.RFC3339Nano)
	if err := journal.Append(r.st, r.fx.Ticket, journal.Line{Slice: "a", Event: journal.EventVerifyPending, Commit: sha, Attempt: 1, TS: old}); err != nil {
		t.Fatal(err)
	}
	spawned := func(pid int, ts string) journal.Line {
		return journal.Line{Ticket: r.fx.Ticket, Slice: "a", Event: journal.EventVerifySpawned, Commit: sha, PID: pid, TS: ts}
	}
	if err := ledger.Append(r.deps.JigHome, spawned(exitedPID(t), old)); err != nil {
		t.Fatal(err)
	}
	// What Spawn writes for the worker it starts, which then runs.
	if err := ledger.Append(r.deps.JigHome, spawned(os.Getpid(), time.Now().Format(time.RFC3339Nano))); err != nil {
		t.Fatal(err)
	}
	res, err := Run(r.deps, r.request("a", sha))
	if err != nil || res.Existing || res.State != journal.VerifyPass || len(r.oracle.seen()) != 1 {
		t.Fatalf("Run = %+v, %v, oracle calls %d, want the oracle to run and pass", res, err, len(r.oracle.seen()))
	}
}

// TestAWorkerSpawnedMomentsAfterTheStaleLineRunsTheOracle: the same, with the
// worker's spawned line written within seconds of the stale pending line.
func TestAWorkerSpawnedMomentsAfterTheStaleLineRunsTheOracle(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	at := time.Now().Add(-3 * time.Second).Format(time.RFC3339Nano)
	if err := journal.Append(r.st, r.fx.Ticket, journal.Line{Slice: "a", Event: journal.EventVerifyPending, Commit: sha, Attempt: 1, TS: at}); err != nil {
		t.Fatal(err)
	}
	for _, pid := range []int{exitedPID(t), os.Getpid()} {
		if err := ledger.Append(r.deps.JigHome, journal.Line{Ticket: r.fx.Ticket, Slice: "a", Event: journal.EventVerifySpawned, Commit: sha, PID: pid, TS: time.Now().Format(time.RFC3339Nano)}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := Run(r.deps, r.request("a", sha))
	if err != nil || res.Existing || len(r.oracle.seen()) != 1 {
		t.Fatalf("Run = %+v, %v, oracle calls %d, want the oracle to run", res, err, len(r.oracle.seen()))
	}
}

// TestStandAsideIgnoresOtherPendingVerifications: another commit's pending line
// in the journal (a hook's worker for the next commit) does not keep a stopped
// verification from running again, and two stale lines each stand aside.
func TestStandAsideIgnoresOtherPendingVerifications(t *testing.T) {
	t.Parallel()
	old := time.Now().Add(-20 * time.Minute).Format(time.RFC3339Nano)
	t.Run("stopped beside a pending commit", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, fixture.Opts{})
		sha := r.fixClamp()
		for _, l := range []journal.Line{
			{Slice: "a", Event: journal.EventOracle, Outcome: "fail", Cause: journal.CauseStopped, Commit: sha, Attempt: 1},
			{Slice: "b", Event: journal.EventVerifyPending, Commit: "feedfeedfeedfeedfeedfeedfeedfeedfeedfeed", Attempt: 1},
		} {
			if err := journal.Append(r.st, r.fx.Ticket, l); err != nil {
				t.Fatal(err)
			}
		}
		res, err := Run(r.deps, r.request("a", sha))
		if err != nil || res.Existing || len(r.oracle.seen()) != 1 {
			t.Fatalf("Run = %+v, %v, oracle calls %d, want the oracle to run", res, err, len(r.oracle.seen()))
		}
	})
	t.Run("two stale lines", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, fixture.Opts{})
		sha := r.fixClamp()
		other := "feedfeedfeedfeedfeedfeedfeedfeedfeedfeed"
		pid := exitedPID(t)
		for _, c := range []struct{ slice, commit string }{{"a", sha}, {"b", other}} {
			if err := journal.Append(r.st, r.fx.Ticket, journal.Line{Slice: c.slice, Event: journal.EventVerifyPending, Commit: c.commit, Attempt: 1, TS: old}); err != nil {
				t.Fatal(err)
			}
			if err := ledger.Append(r.deps.JigHome, journal.Line{Ticket: r.fx.Ticket, Slice: c.slice, Event: journal.EventVerifySpawned, Commit: c.commit, PID: pid, TS: old}); err != nil {
				t.Fatal(err)
			}
		}
		res, err := Run(r.deps, r.request("a", sha))
		if err != nil || res.Existing || len(r.oracle.seen()) != 1 {
			t.Fatalf("Run = %+v, %v, oracle calls %d, want the oracle to run", res, err, len(r.oracle.seen()))
		}
	})
}
