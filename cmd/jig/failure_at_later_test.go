package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestAFailureAtALaterCommitIsTheTipsFailure: slice a commits a1, slice b
// commits b1, and a's oracle runs at b1 and fails. That run covers a1, so a1
// reads failed with its FAIL line in `jig here` and `jig status`, never "no
// recorded result" with a rerun command.
func TestAFailureAtALaterCommitIsTheTipsFailure(t *testing.T) {
	t.Parallel()
	fx, e, st, moveBranch := lastRunRig(t)
	a1 := commitWithTrailer(t, fx, "a.txt", "a")
	b1 := commitWithTrailer(t, fx, "b.txt", "b")
	moveBranch()
	appendLine(t, st, fx.Ticket, journal.Line{Event: journal.EventOracle, Slice: "a", Commit: b1, Outcome: "fail", Attempt: 1, Seconds: 2, Summary: "FAIL TestA"})
	for name, args := range map[string][]string{"jig here": {"here"}, "jig status": {"status", fx.Ticket}} {
		out, code := runMain(t, e, "", args...)
		if code != 0 {
			t.Fatalf("%s: exit %d, output %q", name, code, out)
		}
		var row string
		for _, l := range block(out, "verification_states[") {
			if strings.Contains(l, a1[:12]) {
				row = l
			}
		}
		if !strings.Contains(row, "failed: FAIL TestA") || strings.Contains(row, "no recorded result") {
			t.Errorf("%s: a1's row = %q, want failed with its FAIL line\n%s", name, row, out)
		}
	}
}
