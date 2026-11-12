package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestStatusShowsAnUncountedRun: a slice whose latest oracle run timed out or
// was stopped reads as not counted, a plain failing run shows nothing of the
// kind, and a later pass clears the row.
func TestStatusShowsAnUncountedRun(t *testing.T) {
	t.Parallel()
	fx := newStoreOnlyFixture(t)
	st := openStore(t, fx)
	add := func(l journal.Line) {
		t.Helper()
		if err := journal.Append(st, fx.Ticket, l); err != nil {
			t.Fatal(err)
		}
	}
	const a, b, c = "0123456789abcdef0123456789abcdef01234567", "fedcba9876543210fedcba9876543210fedcba98", "1111111111111111111111111111111111111111"
	add(journal.Line{Slice: "plain", Event: journal.EventOracle, Outcome: "fail", Commit: a, Attempt: 1, Summary: "FAIL TestX"})
	if got := block(statusOf(t, fx, st), "uncounted["); got != nil {
		t.Fatalf("a failing test shows an uncounted block: %v", got)
	}

	add(journal.Line{Slice: "slow", Event: journal.EventOracle, Outcome: "fail", Commit: b, Attempt: 1, Cause: journal.CauseTimeout})
	add(journal.Line{Slice: "halted", Event: journal.EventOracle, Outcome: "fail", Commit: c, Attempt: 1, Cause: journal.CauseStopped})
	got := strings.Join(block(statusOf(t, fx, st), "uncounted["), "\n")
	want := strings.Join([]string{
		"uncounted[2]{slice,commit,run}:",
		`  slow,fedcba987654,"timed out, not counted"`,
		`  halted,111111111111,"stopped, not counted"`,
	}, "\n")
	if got != want {
		t.Errorf("uncounted block:\n%s\nwant:\n%s", got, want)
	}

	add(journal.Line{Slice: "slow", Event: journal.EventOracle, Outcome: "pass", Commit: b, Attempt: 2})
	add(journal.Line{Slice: "slow", Event: journal.EventVerified, Commit: b, Attempt: 2})
	if got := strings.Join(block(statusOf(t, fx, st), "uncounted["), "\n"); !strings.Contains(got, "uncounted[1]") || strings.Contains(got, "slow") {
		t.Errorf("after a pass the block is:\n%s\nwant halted alone", got)
	}
}
