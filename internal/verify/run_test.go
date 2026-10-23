package verify

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/outcome"
	"github.com/develdeco/jig/internal/store"
)

// TestRunPassJournalsWhatTheFrontierDid pins the trail of a passing
// verification: a pending line first, then the oracle line the gate reuses
// (the commit, the exact command, the env class, the time), then verified,
// and the slice green. The oracle ran in a detached worktree of the commit,
// and the worktree is gone afterwards.
func TestRunPassJournalsWhatTheFrontierDid(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	r.oracle.elapsed = 2500 * time.Millisecond
	sha := r.fixClamp()
	// Leave the clone on a later commit: the worktree must hold the commit
	// asked for, not the clone's HEAD.
	r.commit("", "later", map[string]string{"notes.txt": "later"})

	res, err := Run(r.deps, r.request("a", sha))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.State != journal.VerifyPass || res.Existing || res.Attempt != 1 || res.SHA != sha {
		t.Fatalf("Result = %+v, want a first-attempt pass of %s", res, sha)
	}
	if res.Seconds != 3 {
		t.Errorf("Seconds = %d, want 3: 2.5s rounded up", res.Seconds)
	}

	lines := r.lines()
	if got, want := events(lines), []string{"verify-pending", "oracle:pass", "verified"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("journal events = %v, want %v", got, want)
	}
	man, err := manifest.Resolve(r.fx.RepoDir)
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := man.Workspace("alpha")
	wantCmd := man.OracleCmd("test", ws)
	pending, oracle, verified := lines[0], lines[1], lines[2]
	if pending.Slice != "a" || pending.Commit != sha || pending.Attempt != 1 || pending.Session != "s-1" || pending.Agent != "agent-1" {
		t.Errorf("pending line = %+v", pending)
	}
	if oracle.Slice != "a" || oracle.Commit != sha || oracle.Attempt != 1 || oracle.Command != wantCmd || oracle.Env != "" ||
		oracle.Seconds != 3 || oracle.Source != "jig" || oracle.Session != "s-1" || oracle.Agent != "agent-1" ||
		oracle.Summary != "" || oracle.Signature != "" {
		t.Errorf("oracle line = %+v, want a pass of %q at %s", oracle, wantCmd, sha)
	}
	if verified.Slice != "a" || verified.Commit != sha || verified.Attempt != 1 {
		t.Errorf("verified line = %+v", verified)
	}
	if got := journal.BuiltCommits(lines); !reflect.DeepEqual(got, []string{sha}) {
		t.Errorf("BuiltCommits = %v, want the verified commit", got)
	}
	if st := r.state("a"); st.State != "green" || st.Attempts != 1 || st.Reason != "" || st.Signature != "" {
		t.Errorf("slice a state = %+v, want green after one attempt", st)
	}
}

// TestRunPassWorktreeAndOracleEnvironment pins where and how the oracle runs:
// in a detached worktree of the commit asked for (not the clone's HEAD), with
// a recording directory in its environment and the default time limit, and
// the worktree gone afterwards.
func TestRunPassWorktreeAndOracleEnvironment(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	// Leave the clone on a later commit: the worktree must hold the commit
	// asked for, not the clone's HEAD.
	r.commit("", "later", map[string]string{"notes.txt": "later"})

	if _, err := Run(r.deps, r.request("a", sha)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	man, err := manifest.Resolve(r.fx.RepoDir)
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := man.Workspace("alpha")
	wantCmd := man.OracleCmd("test", ws)

	calls := r.oracle.seen()
	if len(calls) != 1 {
		t.Fatalf("the oracle ran %d times, want 1", len(calls))
	}
	call := calls[0]
	if call.cmd != wantCmd || call.limit != manifest.DefaultOracleLimit {
		t.Errorf("oracle ran %q within %s, want %q within the default %s", call.cmd, call.limit, wantCmd, manifest.DefaultOracleLimit)
	}
	if call.head != sha || !call.git || !strings.Contains(call.alpha, "if v > hi") {
		t.Errorf("the oracle ran in %s at HEAD %s (git %v), want a worktree of %s with the fix", call.dir, call.head, call.git, sha)
	}
	if want := r.worktreeDir(sha, "a"); filepath.Clean(call.dir) != filepath.Clean(want) {
		t.Errorf("oracle dir = %s, want the verification's worktree %s", call.dir, want)
	}
	if rd := envValue(call.env, recordDirEnv); rd == "" {
		t.Errorf("the oracle run was given no %s: %v", recordDirEnv, call.env)
	}

	if _, err := os.Stat(r.worktreeDir(sha, "a")); !os.IsNotExist(err) {
		t.Errorf("the worktree is still there: %v", err)
	}
	if got := worktreesOf(t, r.fx.RepoDir); len(got) != 1 {
		t.Errorf("the clone has worktrees %v, want only itself", got)
	}
	if head, _ := gitx.RevParse(r.fx.RepoDir, "HEAD"); head == sha {
		t.Errorf("test setup: the clone's HEAD should be past %s", sha)
	}
}

// TestRunFailPersistsSummaryAndSignature pins the trail of a failing
// verification: the oracle line carries the flattened tail of the output and
// the stall signature built from it, no verified line is written, the slice
// is building, and the 4000-byte tail is in the log.
func TestRunFailPersistsSummaryAndSignature(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	long := strings.Repeat("noise line that scrolls past\n", 300)
	r.oracle.out = long + "--- FAIL: TestClampPercent (0.01s)\n    percent_test.go:7: ClampPercent(150) = 101, want 100\nFAIL\n"
	r.oracle.err = os.ErrInvalid
	sha := r.commit("b", "b: ClampPercent", map[string]string{"alpha/percent.go": "package alpha\n"})

	res, err := Run(r.deps, r.request("b", sha))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.State != journal.VerifyFail || res.Attempt != 1 {
		t.Fatalf("Result = %+v, want a fail on attempt 1", res)
	}
	if got, want := events(r.lines()), []string{"verify-pending", "oracle:fail"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("journal events = %v, want %v and no verified line", got, want)
	}
	oracle := r.lines()[1]
	if len(oracle.Summary) == 0 || len(oracle.Summary) > 600 || strings.ContainsAny(oracle.Summary, "\r\n") {
		t.Errorf("Summary = %q, want one line of at most 600 bytes", oracle.Summary)
	}
	if !strings.Contains(oracle.Summary, "ClampPercent(150) = 101, want 100") || !strings.HasSuffix(oracle.Summary, "FAIL") {
		t.Errorf("Summary = %q, want the end of the output", oracle.Summary)
	}
	if want := outcome.Signature("b", outcome.CodeBug, oracle.Summary); oracle.Signature != want || !strings.HasPrefix(want, "b|code-bug|") {
		t.Errorf("Signature = %q, want %q", oracle.Signature, want)
	}
	if res.Summary != oracle.Summary || res.Signature != oracle.Signature {
		t.Errorf("Result summary/signature = %q / %q, want the line", res.Summary, res.Signature)
	}
	if len(res.Tail) > 4000 || !strings.HasSuffix(res.Tail, "FAIL") || !strings.Contains(res.Tail, "noise line") {
		t.Errorf("Tail is %d bytes, want the last 4000 bytes of the output", len(res.Tail))
	}
	if !strings.Contains(r.log.String(), "ClampPercent(150) = 101, want 100") {
		t.Errorf("the log lacks the oracle output tail:\n%s", r.log.String())
	}
	if st := r.state("b"); st.State != "building" || st.Attempts != 1 || st.Reason != "" || st.Signature != "" {
		t.Errorf("slice b state = %+v, want building after one attempt", st)
	}
	if got := journal.BuiltCommits(r.lines()); len(got) != 0 {
		t.Errorf("BuiltCommits = %v, want none: the commit did not verify", got)
	}
}

// TestRunFailureSignatureIsStableAcrossRuns is what the stall counter relies
// on: the same failure twice yields the same signature, though the digits in
// it (timings, line numbers) and the paths differ.
func TestRunFailureSignatureIsStableAcrossRuns(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	r.oracle.err = os.ErrInvalid
	sha := r.commit("b", "b", map[string]string{"x.txt": "1"})
	r.oracle.out = "--- FAIL: TestX (0.31s)\n    /tmp/run1/x_test.go:12: got 101, want 100\nFAIL\n"
	first, err := Run(r.deps, r.request("b", sha))
	if err != nil {
		t.Fatal(err)
	}
	r.oracle.out = "--- FAIL: TestX (0.07s)\n    /tmp/run2/x_test.go:13: got 102, want 100\nFAIL\n"
	rq := r.request("b", sha)
	rq.Rerun = true
	second, err := Run(r.deps, rq)
	if err != nil {
		t.Fatal(err)
	}
	if first.Signature == "" || first.Signature != second.Signature {
		t.Errorf("signatures differ: %q vs %q", first.Signature, second.Signature)
	}
	if second.Attempt != 2 {
		t.Errorf("the rerun took attempt %d, want 2", second.Attempt)
	}
}

// TestRunFailureWithNoOutputKeepsTheReason: a command that fails and prints
// nothing (not found, killed) still leaves a summary to sign.
func TestRunFailureWithNoOutputKeepsTheReason(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	r.oracle.err = os.ErrNotExist
	sha := r.commit("b", "b", map[string]string{"x.txt": "1"})
	res, err := Run(r.deps, r.request("b", sha))
	if err != nil || res.State != journal.VerifyFail {
		t.Fatalf("Run = %+v, %v, want a fail", res, err)
	}
	if res.Summary != os.ErrNotExist.Error() || res.Signature == "" {
		t.Errorf("Summary = %q, Signature = %q, want the error text", res.Summary, res.Signature)
	}
}

// TestRunIsIdempotent: a verification the journal already holds stands, in
// every state it can be in, and writes nothing; a rerun is the way to do it
// again, as the slice's next attempt.
func TestRunIsIdempotent(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()

	first, err := Run(r.deps, r.request("a", sha))
	if err != nil || first.State != journal.VerifyPass {
		t.Fatalf("first Run = %+v, %v", first, err)
	}
	before := r.lines()

	second, err := Run(r.deps, r.request("a", sha))
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if !second.Existing || second.State != journal.VerifyPass || second.Attempt != 1 {
		t.Errorf("second Result = %+v, want the existing pass of attempt 1", second)
	}
	if after := r.lines(); !reflect.DeepEqual(after, before) {
		t.Errorf("the journal changed on a repeat:\nbefore %v\nafter  %v", events(before), events(after))
	}
	if n := len(r.oracle.seen()); n != 1 {
		t.Errorf("the oracle ran %d times, want once", n)
	}

	// An abbreviated sha is the same commit.
	short, err := Run(r.deps, r.request("a", sha[:10]))
	if err != nil || !short.Existing || short.SHA != sha {
		t.Errorf("Run with an abbreviated sha = %+v, %v, want the existing verification of %s", short, err, sha)
	}

	rerun := r.request("a", sha)
	rerun.Rerun = true
	third, err := Run(r.deps, rerun)
	if err != nil || third.Existing || third.State != journal.VerifyPass || third.Attempt != 2 {
		t.Fatalf("rerun Result = %+v, %v, want a new pass on attempt 2", third, err)
	}
	if got, want := events(r.lines()), []string{"verify-pending", "oracle:pass", "verified", "verify-pending", "oracle:pass", "verified"}; !reflect.DeepEqual(got, want) {
		t.Errorf("journal events = %v, want %v", got, want)
	}
	if st := r.state("a"); st.Attempts != 2 || st.State != "green" {
		t.Errorf("slice a state = %+v, want green after 2 attempts", st)
	}

	// A different slice at the same commit is a verification of its own.
	other, err := Run(r.deps, r.request("c", sha))
	if err != nil || other.Existing || other.Attempt != 1 {
		t.Errorf("slice c at the same commit = %+v, %v, want its own first attempt", other, err)
	}
}

// TestRunStopsAtAPendingRecord: a pending line with no outcome (a worker that
// died, or one on another machine) stands like any other record, and says so.
func TestRunStopsAtAPendingRecord(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	if err := journal.Append(r.st, r.fx.Ticket, journal.Line{Slice: "a", Event: journal.EventVerifyPending, Commit: sha, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	res, err := Run(r.deps, r.request("a", sha))
	if err != nil || !res.Existing || res.State != journal.VerifyPending {
		t.Fatalf("Run = %+v, %v, want the pending record to stand", res, err)
	}
	if len(r.oracle.seen()) != 0 || len(r.lines()) != 1 {
		t.Errorf("a pending record was run over: oracle calls %d, lines %v", len(r.oracle.seen()), events(r.lines()))
	}
}

// TestRunYieldsToAWorkerHoldingTheLock: while another worker holds the
// verification lock a second one runs nothing and records nothing, whatever
// the journal says, rerun or not.
func TestRunYieldsToAWorkerHoldingTheLock(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	release, held, err := store.TryLock(r.worktreeDir(sha, "a"), 0)
	if err != nil || !held {
		t.Fatalf("test setup: lock = %v, %v", held, err)
	}
	defer release()

	for _, rerun := range []bool{false, true} {
		rq := r.request("a", sha)
		rq.Rerun = rerun
		res, err := Run(r.deps, rq)
		if err != nil || !res.Existing || res.State != journal.VerifyPending {
			t.Errorf("Run(rerun=%v) = %+v, %v, want the worker pending state", rerun, res, err)
		}
	}
	if len(r.oracle.seen()) != 0 || len(r.lines()) != 0 {
		t.Errorf("the second worker ran: oracle calls %d, journal %v", len(r.oracle.seen()), events(r.lines()))
	}
}

// TestRunRefusesWhatItCannotSetUp: a request that names no real slice or
// commit is an error and leaves no line behind.
func TestRunRefusesWhatItCannotSetUp(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	for name, rq := range map[string]Request{
		"an unknown slice":  r.request("zz", sha),
		"an unknown commit": r.request("a", "0123456789012345678901234567890123456789"),
		"an option":         r.request("a", "--all"),
		"no ticket":         {Slice: "a", SHA: sha},
		"no slice":          {Ticket: r.fx.Ticket, SHA: sha},
		"no commit":         {Ticket: r.fx.Ticket, Slice: "a"},
	} {
		if _, err := Run(r.deps, rq); err == nil {
			t.Errorf("%s: Run accepted the request", name)
		}
	}
	bad := r.deps
	bad.Clone = ""
	if _, err := Run(bad, r.request("a", sha)); err == nil {
		t.Error("Run with no clone accepted the request")
	}
	if len(r.lines()) != 0 || len(r.oracle.seen()) != 0 {
		t.Errorf("a refused request left a trace: journal %v, oracle calls %d", events(r.lines()), len(r.oracle.seen()))
	}
}
