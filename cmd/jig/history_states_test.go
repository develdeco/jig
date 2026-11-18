package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestStatesLeaveOutWhatTheStopHookCallsHistory: slices a and b verified one
// after the other at HEAD leave a's older commit with no result of its own,
// and `jig here` and `jig status` do not name it (their clearing command would
// send a rerun the covers column and the Stop hook say is not needed), while a
// slice's building tip with no recorded result still shows.
func TestStatesLeaveOutWhatTheStopHookCallsHistory(t *testing.T) {
	t.Parallel()
	fx, e, st, moveBranch := lastRunRig(t)
	a1 := commitWithTrailer(t, fx, "a.txt", "a")
	b1 := commitWithTrailer(t, fx, "b.txt", "b")
	c1 := commitWithTrailer(t, fx, "c.txt", "c")
	moveBranch()
	for _, slice := range []string{"a", "b"} {
		appendLine(t, st, fx.Ticket, journal.Line{Event: journal.EventOracle, Slice: slice, Commit: b1, Outcome: "pass", Attempt: 1, Seconds: 2})
		appendLine(t, st, fx.Ticket, journal.Line{Event: journal.EventVerified, Slice: slice, Commit: b1, Attempt: 1})
	}
	// c's commit is made after both runs and has no result: it is a tip.
	_ = a1
	for name, args := range map[string][]string{"jig here": {"here"}, "jig status": {"status", fx.Ticket}} {
		out, code := runMain(t, e, "", args...)
		if code != 0 {
			t.Fatalf("%s: exit %d, output %q", name, code, out)
		}
		states := strings.Join(block(out, "verification_states["), "\n")
		if strings.Contains(states, a1[:12]) {
			t.Errorf("%s names a's older commit, history once its slice is verified at b's:\n%s", name, states)
		}
		if !strings.Contains(states, c1[:12]) || !strings.Contains(states, "no recorded result") {
			t.Errorf("%s does not name c's tip with no recorded result:\n%s", name, states)
		}
	}
}
