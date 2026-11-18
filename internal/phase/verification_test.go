package phase

import (
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
)

var vNow = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

func vts(ago time.Duration) string { return vNow.Add(-ago).Format(time.RFC3339Nano) }

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	shaC = "cccccccccccccccccccccccccccccccccccccccc"
)

func derive(in VerifyInput) map[string]CommitVerification {
	in.Ticket = "T-1"
	got := map[string]CommitVerification{}
	for _, r := range DeriveVerifications(in) {
		got[r.Commit[:1]] = r
	}
	return got
}

func TestACommitWithNoLineHasNoRecordedResult(t *testing.T) {
	r := derive(VerifyInput{BranchKnown: true, Commits: []CommitRef{{shaA, "s"}}})["a"]
	if r.State != VerifyNoResult || r.Told() != "no recorded result" || r.Clear != "jig verify T-1 --slice s" {
		t.Fatalf("%+v", r)
	}
}

// An old worker's pending line is no result: alone it leaves the commit with
// none, and after a pass or a failure it hides neither.
func TestAnOldPendingLineIsNotAResult(t *testing.T) {
	pending := journal.Line{Event: journal.EventVerifyPending, Slice: "s", Commit: shaA, TS: vts(time.Minute)}
	commits := []CommitRef{{shaA, "s"}}
	cases := []struct {
		name  string
		lines []journal.Line
		want  VerifyState
	}{
		{"alone", []journal.Line{pending}, VerifyNoResult},
		{"after a failure", []journal.Line{{Event: journal.EventOracle, Outcome: "fail", Slice: "s", Commit: shaA, Summary: "FAIL x"}, pending}, VerifyFailed},
		{"after a pass", []journal.Line{{Event: journal.EventOracle, Outcome: "pass", Slice: "s", Commit: shaA}, pending}, VerifyPass},
	}
	for _, c := range cases {
		if r := derive(VerifyInput{BranchKnown: true, Lines: c.lines, Commits: commits})["a"]; r.State != c.want {
			t.Errorf("%s: %+v", c.name, r)
		}
	}
	// Off the branch, a journal that holds only a pending line names no pair.
	if got := DeriveVerifications(VerifyInput{Ticket: "T-1", Lines: []journal.Line{pending}}); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestEachCommitIsJudgedOnItsOwn(t *testing.T) {
	lines := []journal.Line{
		{Event: journal.EventOracle, Outcome: "fail", Slice: "s", Commit: shaA, Summary: "x"},
		{Event: journal.EventOracle, Outcome: "pass", Slice: "s", Commit: shaB},
	}
	got := derive(VerifyInput{BranchKnown: true, Lines: lines, Commits: []CommitRef{{shaA, "s"}, {shaB, "s"}}})
	if got["a"].State != VerifyFailed || got["b"].State != VerifyPass {
		t.Fatalf("%+v", got)
	}
}

func TestStoppedAndTimedOutAreFailedRunsAndErrorCouldNotRun(t *testing.T) {
	lines := []journal.Line{
		{Event: journal.EventOracle, Outcome: "fail", Cause: journal.CauseStopped, Slice: "s", Commit: shaA},
		{Event: journal.EventVerifyError, Outcome: "error", Slice: "s", Commit: shaB, Summary: "no worktree"},
		{Event: journal.EventOracle, Outcome: "fail", Cause: journal.CauseTimeout, Slice: "s", Commit: shaC},
	}
	got := derive(VerifyInput{BranchKnown: true, Lines: lines, Commits: []CommitRef{{shaA, "s"}, {shaB, "s"}, {shaC, "s"}}})
	if got["a"].State != VerifyFailed || got["b"].State != VerifyCouldNotRun || got["c"].State != VerifyFailed {
		t.Fatalf("%+v", got)
	}
	if got["a"].Told() != "stopped, not counted" || got["c"].Told() != "timed out, not counted" || got["b"].Told() != "could not run" {
		t.Fatalf("words: %+v", got)
	}
	if got["b"].Error != "no worktree" || got["a"].Clear != "jig verify T-1 --slice s" {
		t.Fatalf("%+v", got)
	}
}

func TestFailedNamesItsFirstFailLine(t *testing.T) {
	cases := map[string]string{
		"ok pkg/a 0.1s --- FAIL: TestX (0.00s) boom FAIL pkg/b": "--- FAIL: TestX (0.00s) boom FAIL pkg/b",
		"noise\nFAIL pkg/b 0.2s\nmore":                          "FAIL pkg/b 0.2s",
		"\n  first thing\nsecond":                               "first thing",
		"nothing to see here":                                   "nothing to see here",
		"":                                                      "",
	}
	for in, want := range cases {
		if got := FailLine(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestACommitWithNoRecordedResultHoldsAndNothingElseDoes(t *testing.T) {
	for s, want := range map[VerifyState]bool{VerifyNoResult: true, VerifyFailed: false, VerifyCouldNotRun: false, VerifyPass: false} {
		if s.Holds() != want {
			t.Errorf("%s", s)
		}
	}
}

func TestWithNoBranchTheJournalsPairsAreJudged(t *testing.T) {
	lines := []journal.Line{{Event: journal.EventOracle, Outcome: "fail", Slice: "s", Commit: shaA, Summary: "FAIL x"}}
	r := derive(VerifyInput{Lines: lines})["a"]
	if r.State != VerifyFailed || r.FailLine != "FAIL x" {
		t.Fatalf("%+v", r)
	}
}
