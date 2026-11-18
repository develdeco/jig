package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestHereAndStatusTellEachStateInItsWords: a commit whose run was stopped or
// timed out says so in those words (a recorded failed run that judged
// nothing, never "failed: ..."), and one whose verification could not run says
// why, in the text of `jig here` and `jig status` and in `--json`'s told.
func TestHereAndStatusTellEachStateInItsWords(t *testing.T) {
	t.Parallel()
	fx, e, st, moveBranch := lastRunRig(t)
	stopped := commitWithTrailer(t, fx, "one.txt", "a")
	timedOut := commitWithTrailer(t, fx, "two.txt", "b")
	errored := commitWithTrailer(t, fx, "three.txt", "c")
	moveBranch()
	appendLine(t, st, fx.Ticket, journal.Line{Event: journal.EventOracle, Slice: "a", Commit: stopped, Outcome: "fail", Cause: journal.CauseStopped, Attempt: 1, Summary: "FAIL TestA"})
	appendLine(t, st, fx.Ticket, journal.Line{Event: journal.EventOracle, Slice: "b", Commit: timedOut, Outcome: "fail", Cause: journal.CauseTimeout, Attempt: 1, Summary: "FAIL TestB"})
	appendLine(t, st, fx.Ticket, journal.Line{Event: journal.EventVerifyError, Slice: "c", Commit: errored, Attempt: 1, Summary: "no worktree"})

	want := map[string]string{"a": "stopped, not counted", "b": "timed out, not counted", "c": "could not run: no worktree"}
	check := func(where, out string) {
		t.Helper()
		table := strings.Join(block(out, "verification_states["), "\n")
		for slice, words := range want {
			found := false
			for _, row := range strings.Split(table, "\n") {
				if strings.HasPrefix(strings.TrimSpace(row), slice+",") && strings.Contains(row, words) {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: no row for slice %s says %q:\n%s", where, slice, words, table)
			}
		}
		if strings.Contains(table, "failed:") {
			t.Errorf("%s: a run that judged nothing reads as failed:\n%s", where, table)
		}
	}
	out, code := runMain(t, e, "", "here")
	if code != 0 {
		t.Fatalf("jig here: exit %d, output %q", code, out)
	}
	check("jig here", out)
	out, code = runMain(t, e, "", "status", fx.Ticket)
	if code != 0 {
		t.Fatalf("jig status: exit %d, output %q", code, out)
	}
	check("jig status", out)

	out, _ = runMain(t, e, "", "here", "--json")
	var got statesJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	told := map[string]string{}
	for _, r := range got.State.VerificationStates {
		told[r.Slice] = r.Told
	}
	for slice, words := range map[string]string{"a": "stopped, not counted", "b": "timed out, not counted", "c": "could not run"} {
		if told[slice] != words {
			t.Errorf("--json told for %s = %q, want %q", slice, told[slice], words)
		}
	}
}

// TestStatusLastRunsCostNoGitLogOfTheirOwn: `jig status` reads the ticket's
// branch once for both of its sections that need it, so slices with a last
// run add no git process to the ones that name the commits without a result.
func TestStatusLastRunsCostNoGitLogOfTheirOwn(t *testing.T) {
	fx, e, st, moveBranch := lastRunRig(t)
	shas := map[string]string{}
	for _, slice := range []string{"a", "b", "c", "d"} {
		shas[slice] = commitWithTrailer(t, fx, slice+".txt", slice)
	}
	moveBranch()
	run := func() int {
		processes, _ := measureWork(t, func() {
			if out, code := runMain(t, e, "", "status", fx.Ticket); code != 0 {
				t.Fatalf("jig status: exit %d, output %q", code, out)
			}
		})
		return processes
	}
	without := run()
	for slice, sha := range shas {
		appendLine(t, st, fx.Ticket, journal.Line{Event: journal.EventOracle, Slice: slice, Commit: sha, Outcome: "pass", Attempt: 1, Seconds: 1})
	}
	with := run()
	t.Logf("jig status: %d git processes with no last run, %d with four", without, with)
	// Both sections read the branch whether or not a slice ran, so the two
	// counts alone cannot tell a shared read from two: one git log is the whole
	// of what the status starts here.
	if with > without || with > 1 {
		t.Errorf("jig status started %d git processes (%d with no last run), want one git log shared by its sections", with, without)
	}
}
