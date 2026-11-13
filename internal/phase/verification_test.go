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
	in.Ticket, in.Now = "T-1", vNow
	got := map[string]CommitVerification{}
	for _, r := range DeriveVerifications(in) {
		got[r.Commit[:1]] = r
	}
	return got
}

func TestACommitWithNoLineIsNotVerified(t *testing.T) {
	r := derive(VerifyInput{BranchKnown: true, Commits: []CommitRef{{shaA, "s"}}})["a"]
	if r.State != VerifyNotVerified || r.Clear != "jig verify T-1 aaaaaaaaaaaa --slice s --wait" {
		t.Fatalf("%+v", r)
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

func TestPendingWithDeadWorkerIsStaleAndLiveIsRunning(t *testing.T) {
	lines := []journal.Line{{Event: journal.EventVerifyPending, Slice: "s", Commit: shaA, TS: vts(time.Minute)}}
	at := vNow.Add(-time.Minute)
	in := VerifyInput{BranchKnown: true, Lines: lines, Commits: []CommitRef{{shaA, "s"}},
		Spawned: map[CommitRef][]Spawned{{shaA, "s"}: {{PID: 7, At: at}}}}
	in.Alive = func(pid int, nb time.Time) bool { return false }
	if r := derive(in)["a"]; r.State != VerifyStale || r.Clear == "" {
		t.Fatalf("%+v", r)
	}
	in.Alive = func(pid int, nb time.Time) bool { return pid == 7 && nb.Equal(at) }
	if r := derive(in)["a"]; r.State != VerifyRunning || r.Clear != "" {
		t.Fatalf("%+v", r)
	}
}

func TestPendingWithNoSpawnedLineUsesTheAgeRule(t *testing.T) {
	for age, want := range map[time.Duration]VerifyState{time.Hour: VerifyRunning, 3 * time.Hour: VerifyStale} {
		lines := []journal.Line{{Event: journal.EventVerifyPending, Slice: "s", Commit: shaA, TS: vts(age)}}
		r := derive(VerifyInput{BranchKnown: true, Lines: lines, Commits: []CommitRef{{shaA, "s"}},
			Alive: func(int, time.Time) bool { return false }})["a"]
		if r.State != want {
			t.Fatalf("age %v: %+v", age, r)
		}
	}
}

func TestStoppedErrorAndTimedOutAreToldApart(t *testing.T) {
	lines := []journal.Line{
		{Event: journal.EventOracle, Outcome: "fail", Cause: journal.CauseStopped, Slice: "s", Commit: shaA},
		{Event: journal.EventVerifyError, Outcome: "error", Slice: "s", Commit: shaB, Summary: "no worktree"},
		{Event: journal.EventOracle, Outcome: "fail", Cause: journal.CauseTimeout, Slice: "s", Commit: shaC},
	}
	got := derive(VerifyInput{BranchKnown: true, Lines: lines, Commits: []CommitRef{{shaA, "s"}, {shaB, "s"}, {shaC, "s"}}})
	if got["a"].State != VerifyStopped || got["b"].State != VerifyStopped || got["c"].State != VerifyTimedOut {
		t.Fatalf("%+v", got)
	}
	if got["c"].State.StandsAside() || !got["a"].State.StandsAside() {
		t.Fatal("stood aside wrongly")
	}
	if got["a"].State.Told() != "stopped, not counted" || got["c"].State.Told() != "timed out, not counted" {
		t.Fatal("words")
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

func TestHoldsOnlyOnNotVerifiedAndStale(t *testing.T) {
	for s, want := range map[VerifyState]bool{VerifyNotVerified: true, VerifyStale: true, VerifyFailed: false, VerifyStopped: false, VerifyRunning: false, VerifyPass: false} {
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

func TestAPendingLineOffTheBranchIsStillJudged(t *testing.T) {
	lines := []journal.Line{{Event: journal.EventVerifyPending, Slice: "s", Commit: shaB, TS: vts(time.Minute)}}
	got := derive(VerifyInput{BranchKnown: true, Lines: lines, Commits: []CommitRef{{shaA, "s"}}})
	if got["a"].State != VerifyNotVerified || got["b"].State != VerifyRunning {
		t.Fatalf("%+v", got)
	}
}

func TestAnEarlierRunsSpawnedLineDoesNotJudgeALaterPendingLine(t *testing.T) {
	// A run in process leaves no spawned line: the dead worker of an earlier
	// run of the commit must not make its pending line stale.
	lines := []journal.Line{{Event: journal.EventVerifyPending, Slice: "s", Commit: shaA, TS: vts(time.Minute), Attempt: 2}}
	in := VerifyInput{BranchKnown: true, Lines: lines, Commits: []CommitRef{{shaA, "s"}},
		Spawned: map[CommitRef][]Spawned{{shaA, "s"}: {{PID: 7, At: vNow.Add(-20 * time.Minute)}}},
		Alive:   func(int, time.Time) bool { return false }}
	if r := derive(in)["a"]; r.State != VerifyRunning {
		t.Fatalf("%+v", r)
	}
}
