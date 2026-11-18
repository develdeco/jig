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
)

// TestRunRunsTheOracleInTheCallersDirectory pins where and how the oracle
// runs: in the worktree it is given, which is where HEAD is (jig makes no
// checkout of its own), with a recording directory in its environment and no
// time limit, and nothing left behind in the clone's worktrees.
func TestRunRunsTheOracleInTheCallersDirectory(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()

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
	if call.cmd != wantCmd || call.limit != 0 {
		t.Errorf("oracle ran %q within %s, want %q with no limit: the harness owns time", call.cmd, call.limit, wantCmd)
	}
	if filepath.Clean(call.dir) != filepath.Clean(r.fx.RepoDir) || call.head != sha || !strings.Contains(call.alpha, "if v > hi") {
		t.Errorf("the oracle ran in %s at HEAD %s, want the caller's directory %s at %s with the fix", call.dir, call.head, r.fx.RepoDir, sha)
	}
	if rd := envValue(call.env, recordDirEnv); rd == "" {
		t.Errorf("the oracle run was given no %s: %v", recordDirEnv, call.env)
	}
	if len(r.oracle.seen()) != 1 {
		t.Errorf("the oracle ran more than once")
	}
}

// TestRunTimeoutBoundsTheCommandOnlyWhenAsked: the manifest's own limit does
// not apply, and a timeout in the request is the bound.
func TestRunTimeoutBoundsTheCommandOnlyWhenAsked(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	rq := r.request("a", sha)
	rq.Timeout = 90 * time.Second
	if _, err := Run(r.deps, rq); err != nil {
		t.Fatal(err)
	}
	if calls := r.oracle.seen(); len(calls) != 1 || calls[0].limit != 90*time.Second {
		t.Errorf("calls = %+v, want one bounded by the request's timeout", calls)
	}
}

// TestACleanPassAtAnUnchangedHeadCoversTheCommit pins the trail of a passing
// run: the oracle line that holds every input and fact needed to run it again
// (the commit, the exact command, the directory, the flags, HEAD at both ends,
// the tree before and after, the time), then verified, and the slice green.
// No pending line is written: nothing is pending while a foreground command
// runs.
func TestACleanPassAtAnUnchangedHeadCoversTheCommit(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	r.oracle.elapsed = 2500 * time.Millisecond
	sha := r.fixClamp()

	rq := r.request("a", sha)
	rq.Flags = "--slice a"
	res, err := Run(r.deps, rq)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.State != journal.VerifyPass || !res.Covered || res.Uncovered != "" || res.Attempt != 1 || res.SHA != sha || res.Code != 0 || !res.Ran {
		t.Fatalf("Result = %+v, want a covering first-attempt pass of %s", res, sha)
	}
	if res.Seconds != 3 {
		t.Errorf("Seconds = %d, want 3: 2.5s rounded up", res.Seconds)
	}

	lines := r.lines()
	if got, want := events(lines), []string{"oracle:pass", "verified"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("journal events = %v, want %v", got, want)
	}
	man, err := manifest.Resolve(r.fx.RepoDir)
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := man.Workspace("alpha")
	wantCmd := man.OracleCmd("test", ws)
	oracle, verified := lines[0], lines[1]
	if oracle.Slice != "a" || oracle.Commit != sha || oracle.Attempt != 1 || oracle.Command != wantCmd || oracle.Env != "" ||
		oracle.Seconds != 3 || oracle.Source != "jig" || oracle.Session != "s-1" || oracle.Agent != "agent-1" ||
		oracle.Summary != "" || oracle.Signature != "" || oracle.Uncovered != "" || oracle.Subject != "fix Clamp" {
		t.Errorf("oracle line = %+v, want a pass of %q at %s", oracle, wantCmd, sha)
	}
	if filepath.Clean(oracle.Dir) != filepath.Clean(r.fx.RepoDir) || oracle.Flags != "--slice a" || oracle.HeadEnd != sha ||
		oracle.TreeBefore != journal.TreeClean || oracle.TreeAfter != journal.TreeClean || oracle.Jig == "" {
		t.Errorf("oracle line = %+v, want the directory, the flags, HEAD at both ends, a clean tree both times and the jig that ran", oracle)
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
	if v := journal.VerificationState(lines, sha, "a"); v.State != journal.VerifyPass {
		t.Errorf("VerificationState = %+v, want a pass", v)
	}
}

// TestADirtyTreeOrAMovedHeadCoversNothing: a run is recorded whatever it was,
// and covers HEAD only when the tree was clean before and after, HEAD did not
// move, the command was the slice's own oracle and HEAD descends from the
// start sha. Anything else writes the oracle line with the reason, which
// every reader of verification state skips, and no verified line.
func TestADirtyTreeOrAMovedHeadCoversNothing(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		setup  func(r *rig)
		during func(r *rig)
		cmd    string
		want   string
	}{
		{name: "a dirty tree before", setup: func(r *rig) { writeIn(t, r, "scratch.txt", "x") }, want: journal.UncoveredDirtyBefore},
		{name: "a file left behind", during: func(r *rig) { writeIn(t, r, "left.txt", "x") }, want: journal.UncoveredDirtyAfter},
		{name: "a moved HEAD", during: func(r *rig) { r.commit("", "moved", map[string]string{"m.txt": "1"}) }, want: journal.UncoveredHeadMoved},
		{name: "another command", cmd: "echo something else", want: journal.UncoveredOtherCmd},
		{name: "a start sha it does not descend from", setup: func(r *rig) {
			r.deps.Repo = repoName
			later := r.commit("", "later", map[string]string{"later.txt": "x"})
			if err := r.st.WriteStartSHA(r.fx.Ticket, repoName, later); err != nil {
				t.Fatal(err)
			}
		}, want: journal.UncoveredNotDescend},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, fixture.Opts{})
			sha := r.fixClamp()
			if c.setup != nil {
				c.setup(r)
				sha, _ = gitx.RevParse(r.fx.RepoDir, "HEAD")
			}
			if c.during != nil {
				r.oracle.during = func(string) { c.during(r) }
			}
			// The run records scenarios; a run that covers nothing keeps none.
			r.oracle.files = map[string]string{"login.svg": "<svg xmlns='http://www.w3.org/2000/svg'/>"}
			rq := r.request("a", sha)
			rq.Command = c.cmd
			res, err := Run(r.deps, rq)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if res.Covered || res.Uncovered != c.want || res.State != journal.VerifyPass || !res.Ran {
				t.Fatalf("Result = %+v, want a pass that covers nothing: %s", res, c.want)
			}
			if got, want := events(r.lines()), []string{"oracle:pass"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("journal events = %v, want %v and no verified line", got, want)
			}
			line := r.lines()[0]
			if line.Uncovered != c.want {
				t.Errorf("oracle line = %+v, want the reason %q", line, c.want)
			}
			if v := journal.VerificationState(r.lines(), sha, "a"); v.State != journal.VerifyNone {
				t.Errorf("VerificationState = %+v, want none: an uncovered line says nothing of a commit", v)
			}
			if got := journal.BuiltCommits(r.lines()); len(got) != 0 {
				t.Errorf("BuiltCommits = %v, want none", got)
			}
		})
	}
}

// writeIn writes a file in the rig's worktree.
func writeIn(t *testing.T, r *rig, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.fx.RepoDir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestAnUncoveredRunChangesNoSliceState: a run that covers nothing leaves the
// slice as it was, whether it passed or failed, and a covering run after it is
// the first attempt that counts.
func TestAnUncoveredRunChangesNoSliceState(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	before := r.state("a")

	for _, failing := range []bool{false, true} {
		r.oracle.err = nil
		if failing {
			r.oracle.err = os.ErrInvalid
		}
		rq := r.request("a", sha)
		rq.Command = "echo not the oracle"
		res, err := Run(r.deps, rq)
		if err != nil || res.Covered {
			t.Fatalf("Run = %+v, %v, want an uncovered run", res, err)
		}
		if after := r.state("a"); !reflect.DeepEqual(after, before) {
			t.Errorf("slice a state = %+v after an uncovered run (failing %v), want %+v", after, failing, before)
		}
	}
	for _, l := range r.lines() {
		if l.Event == journal.EventVerified {
			t.Errorf("an uncovered run wrote %+v", l)
		}
	}
	r.oracle.err = nil
	if res, err := Run(r.deps, r.request("a", sha)); err != nil || !res.Covered {
		t.Fatalf("covering Run = %+v, %v", res, err)
	}
	if st := r.state("a"); st.State != "green" {
		t.Errorf("slice a state = %+v, want green after the covering run", st)
	}
}

// TestRunFailPersistsSummaryAndSignature pins the trail of a failing run: the
// oracle line carries the flattened tail of the output and the stall
// signature built from it, no verified line is written, the slice is
// building, and the output reached the stream whole.
func TestRunFailPersistsSummaryAndSignature(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	var out strings.Builder
	r.deps.Out = &out
	long := strings.Repeat("noise line that scrolls past\n", 300)
	r.oracle.out = long + "--- FAIL: TestClampPercent (0.01s)\n    percent_test.go:7: ClampPercent(150) = 101, want 100\nFAIL\n"
	r.oracle.err = os.ErrInvalid
	sha := r.commit("b", "b: ClampPercent", map[string]string{"alpha/percent.go": "package alpha\n"})

	res, err := Run(r.deps, r.request("b", sha))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.State != journal.VerifyFail || res.Attempt != 1 || !res.Covered || res.Code != 1 {
		t.Fatalf("Result = %+v, want a covering fail on attempt 1 with the command's exit code", res)
	}
	if got, want := events(r.lines()), []string{"oracle:fail"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("journal events = %v, want %v and no verified line", got, want)
	}
	oracle := r.lines()[0]
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
	if !strings.HasPrefix(out.String(), "noise line") || !strings.Contains(out.String(), "ClampPercent(150) = 101, want 100") || len(out.String()) < len(long) {
		t.Errorf("the stream holds %d bytes, want the command's output unfiltered", out.Len())
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
	second, err := Run(r.deps, r.request("b", sha))
	if err != nil {
		t.Fatal(err)
	}
	if first.Signature == "" || first.Signature != second.Signature {
		t.Errorf("signatures differ: %q vs %q", first.Signature, second.Signature)
	}
	if second.Attempt != 2 {
		t.Errorf("the second run took attempt %d, want 2", second.Attempt)
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

// TestRunRunsAgainEveryTime: a second run of a commit that has a pass runs the
// oracle again, as the slice's next attempt; nothing stands in its way, no
// lock, no existing answer, no pending record.
func TestRunRunsAgainEveryTime(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()

	for attempt := 1; attempt <= 2; attempt++ {
		res, err := Run(r.deps, r.request("a", sha))
		if err != nil || res.State != journal.VerifyPass || res.Attempt != attempt {
			t.Fatalf("Run %d = %+v, %v, want a pass on attempt %d", attempt, res, err, attempt)
		}
	}
	if got, want := events(r.lines()), []string{"oracle:pass", "verified", "oracle:pass", "verified"}; !reflect.DeepEqual(got, want) {
		t.Errorf("journal events = %v, want %v", got, want)
	}
	if n := len(r.oracle.seen()); n != 2 {
		t.Errorf("the oracle ran %d times, want twice", n)
	}
	if st := r.state("a"); st.Attempts != 2 || st.State != "green" {
		t.Errorf("slice a state = %+v, want green after 2 attempts", st)
	}
	// A pending line an older jig left is no obstacle either.
	other := r.commit("c", "c", map[string]string{"c.txt": "1"})
	if err := journal.Append(r.st, r.fx.Ticket, journal.Line{Slice: "c", Event: journal.EventVerifyPending, Commit: other, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	if res, err := Run(r.deps, r.request("c", other)); err != nil || res.State != journal.VerifyPass {
		t.Errorf("Run over an old pending line = %+v, %v, want a pass", res, err)
	}
}

// TestRunRefusesWhatItCannotSetUp: a request that names no real slice or
// ticket, and a run with no worktree, is an error and leaves no line behind.
func TestRunRefusesWhatItCannotSetUp(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	for name, rq := range map[string]Request{
		"an unknown slice": r.request("zz", sha),
		"no ticket":        {Slice: "a"},
		"no slice":         {Ticket: r.fx.Ticket},
	} {
		if _, err := Run(r.deps, rq); err == nil {
			t.Errorf("%s: Run accepted the request", name)
		}
	}
	bad := r.deps
	bad.Clone = ""
	if _, err := Run(bad, r.request("a", sha)); err == nil {
		t.Error("Run with no worktree accepted the request")
	}
	if len(r.lines()) != 0 || len(r.oracle.seen()) != 0 {
		t.Errorf("a refused request left a trace: journal %v, oracle calls %d", events(r.lines()), len(r.oracle.seen()))
	}
}

// TestRunWithNoRecordWritesNothing: the command runs and its result is
// returned, and no line, state or push is written.
func TestRunWithNoRecordWritesNothing(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	before := r.state("a")
	rq := r.request("a", sha)
	rq.NoRecord = true
	res, err := Run(r.deps, rq)
	if err != nil || res.State != journal.VerifyPass || res.Pushed || len(res.Recorded) != 0 {
		t.Fatalf("Run = %+v, %v, want a pass that recorded nothing", res, err)
	}
	if len(r.lines()) != 0 || !reflect.DeepEqual(r.state("a"), before) {
		t.Errorf("--no-record wrote %v, state %+v", events(r.lines()), r.state("a"))
	}
	if rd := envValue(r.oracle.seen()[0].env, recordDirEnv); rd != "" {
		t.Errorf("a run that records nothing was given a recording directory: %s", rd)
	}
}

// TestPlanSaysWhatARunWouldDoAndDoesNothing: the command, the directory, the
// commit and the tree are known before the run, and nothing runs or is written.
func TestPlanSaysWhatARunWouldDoAndDoesNothing(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	writeIn(t, r, "scratch.txt", "x")
	res, err := Plan(r.deps, r.request("a", sha))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if res.SHA != sha || res.Command == "" || res.Attempt != 1 || res.TreeBefore != journal.TreeDirty || res.Uncovered != journal.UncoveredDirtyBefore || res.Covered {
		t.Errorf("Plan = %+v, want the commit, the command, attempt 1 and a run that would cover nothing: a dirty tree", res)
	}
	if len(r.oracle.seen()) != 0 || len(r.lines()) != 0 {
		t.Errorf("Plan ran or wrote: oracle calls %d, journal %v", len(r.oracle.seen()), events(r.lines()))
	}
}
