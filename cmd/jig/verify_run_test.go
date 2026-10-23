package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/outcome"
)

// applyScenarioPatch applies slice's attempt-1 patch from the fixture scenario
// to the clone and commits it with the slice's trailer.
func (r *verifyRig) applyScenarioPatch(slice string) string {
	r.t.Helper()
	patch := filepath.Join(r.fx.ScenarioDir, "slices", slice, "attempt-1", "patch.diff")
	gitMust(r.t, r.fx.RepoDir, "apply", patch)
	gitMust(r.t, r.fx.RepoDir, "add", "-A")
	gitMust(r.t, r.fx.RepoDir, "commit", "-m", slice+": scenario patch", "-m", "Jig-Slice: "+slice)
	return gitMust(r.t, r.fx.RepoDir, "rev-parse", "HEAD")
}

// logOf reads the verification's log.
func (r *verifyRig) logOf(sha, slice string) string {
	r.t.Helper()
	id, err := r.st.ID()
	if err != nil {
		r.t.Fatal(err)
	}
	path, err := home.VerifyLogPath(r.fx.Home, id, r.fx.Ticket, sha, slice)
	if err != nil {
		r.t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		r.t.Fatalf("read the log %s: %v", path, err)
	}
	return string(data)
}

// TestVerifyWaitRunsTheOracleHereAndReportsThePass: --wait runs the real
// oracle in the detached worktree in this process, prints the outcome, exits 0
// on a pass, and leaves the frontier's lines and the slice green.
func TestVerifyWaitRunsTheOracleHereAndReportsThePass(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	sha := r.fixClamp()

	out, code := r.run("verify", "--wait")
	if code != 0 {
		t.Fatalf("verify --wait exit = %d, want 0\n%s", code, out)
	}
	for _, want := range []string{"state: pass", "slice: a", "commit: " + sha, "attempt: 1", "seconds: "} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	var got []string
	for _, l := range r.journal() {
		got = append(got, l.Event+":"+l.Outcome)
	}
	if want := "verify-pending: oracle:pass verified:"; strings.Join(got, " ") != want {
		t.Errorf("journal events = %v, want %s", got, want)
	}
	if ss, err := r.st.ReadSliceState(r.fx.Ticket, "a"); err != nil || ss.State != "green" {
		t.Errorf("slice a state = %+v, %v, want green", ss, err)
	}
	if log := r.logOf(sha, "a"); !strings.Contains(log, "verifying") {
		t.Errorf("the log lacks the run:\n%s", log)
	}
	if n := len(r.spawn.Specs()); n != 0 {
		t.Errorf("--wait started %d processes, want none", n)
	}
}

// TestVerifyWaitReportsAFailureWithItsSignature: a failing oracle is exit 1,
// the report carries the summary and signature the journal line carries, the
// slice is building, and the log holds the output tail.
func TestVerifyWaitReportsAFailureWithItsSignature(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	// Slice b's patch, without a's fix under it: its test fails.
	sha := r.applyScenarioPatch("b")

	out, code := r.run("verify", "JIG-1", sha, "--wait")
	if code != 1 {
		t.Fatalf("verify --wait exit = %d, want 1 for a failing oracle\n%s", code, out)
	}
	if !strings.Contains(out, "state: fail") || !strings.Contains(out, "summary: ") || !strings.Contains(out, "signature: ") {
		t.Errorf("output lacks the failure's state, summary and signature:\n%s", out)
	}
	var oracle journal.Line
	for _, l := range r.journal() {
		if l.Event == journal.EventOracle {
			oracle = l
		}
	}
	if oracle.Outcome != "fail" || oracle.Summary == "" || oracle.Signature != outcome.Signature("b", outcome.CodeBug, oracle.Summary) || oracle.Attempt != 1 {
		t.Errorf("oracle line = %+v, want a failure with its summary and signature on attempt 1", oracle)
	}
	if ss, err := r.st.ReadSliceState(r.fx.Ticket, "b"); err != nil || ss.State != "building" {
		t.Errorf("slice b state = %+v, %v, want building", ss, err)
	}
	if log := r.logOf(sha, "b"); !strings.Contains(log, "FAIL") {
		t.Errorf("the log lacks the oracle output tail:\n%s", log)
	}
}

// TestVerifyWorkerVerifiesInProcessAndWritesItsReportToStdout: the hidden
// worker mode is the same verification with stdout as the log; it exits 0 for a
// failed oracle (the worker did its job) and 1 for a run that could not reach
// one.
func TestVerifyWorkerVerifiesInProcessAndWritesItsReportToStdout(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	sha := r.applyScenarioPatch("b")

	out, code := r.run("verify", "--worker", "--store", r.st.Root, "--repo-dir", r.fx.RepoDir, "--slice", "b", "--session", "s-9", "--agent-id", "ag-1", "JIG-1", sha)
	if code != 0 {
		t.Fatalf("worker exit = %d, want 0 for a failed oracle\n%s", code, out)
	}
	for _, want := range []string{"verify: verifying", "the oracle failed", "state: fail"} {
		if !strings.Contains(out, want) {
			t.Errorf("worker stdout lacks %q:\n%s", want, out)
		}
	}
	for _, l := range r.journal() {
		if l.Event == journal.EventVerifyPending && (l.Session != "s-9" || l.Agent != "ag-1") {
			t.Errorf("pending line = %+v, want the session and agent the worker was given", l)
		}
	}

	// A commit that does not descend from the start sha cannot be verified.
	if err := r.st.WriteStartSHA(r.fx.Ticket, "fixture-repo", sha); err != nil {
		t.Fatal(err)
	}
	out, code = r.run("verify", "--worker", "--store", r.st.Root, "--repo-dir", r.fx.RepoDir, "--slice", "b", "--rerun", "JIG-1", sha)
	if code != 1 || !strings.Contains(out, "state: error") || !strings.Contains(out, "no new work") {
		t.Errorf("worker over a commit that is the start sha: exit %d, want 1 and an error saying so\n%s", code, out)
	}
}

// TestVerifyWaitWaitsForTheWorkerAlreadyOnTheCommit: a pending record means a
// worker (the git hook started one, say) is on it; --wait waits for its outcome
// rather than reporting "pending" and leaving.
func TestVerifyWaitWaitsForTheWorkerAlreadyOnTheCommit(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	sha := r.fixClamp()
	if err := journal.Append(r.st, r.fx.Ticket, journal.Line{Slice: "a", Event: journal.EventVerifyPending, Commit: sha, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		time.Sleep(300 * time.Millisecond)
		done <- journal.Append(r.st, r.fx.Ticket, journal.Line{Slice: "a", Event: journal.EventOracle, Outcome: "pass", Commit: sha, Attempt: 1, Seconds: 2})
	}()

	out, code := r.run("verify", "JIG-1", sha, "--wait")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if code != 0 || !strings.Contains(out, "state: pass") || !strings.Contains(out, "existing: true") {
		t.Errorf("verify --wait over a pending record: exit %d, want the worker's pass\n%s", code, out)
	}
	if n := len(r.spawn.Specs()); n != 0 {
		t.Errorf("started %d processes, want none", n)
	}
}

// TestVerifyWaitStopsWaitingWhenInterrupted: the first interrupt cancels the
// env's context and the wait ends with the verification still pending.
func TestVerifyWaitStopsWaitingWhenInterrupted(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	sha := r.fixClamp()
	if err := journal.Append(r.st, r.fx.Ticket, journal.Line{Slice: "a", Event: journal.EventVerifyPending, Commit: sha, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.e = r.e.withContext(ctx)

	out, code := r.run("verify", "JIG-1", sha, "--wait")
	if code != 0 || !strings.Contains(out, "state: pending") || !strings.Contains(out, "Still pending") {
		t.Errorf("an interrupted wait: exit %d\n%s", code, out)
	}
}
