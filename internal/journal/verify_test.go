package journal

import "testing"

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// TestVerificationState reads the latest record of one (commit, slice) from
// the lines that mention it: the digester's verify-error line and the
// frontier's oracle and verified lines alike, the last in journal order
// winning. A verify-pending line is no result and is skipped.
func TestVerificationState(t *testing.T) {
	t.Parallel()
	pending := Line{TS: "t1", Slice: "a", Event: EventVerifyPending, Commit: shaA, Attempt: 1, Session: "s1"}
	passed := Line{TS: "t2", Slice: "a", Event: EventOracle, Outcome: "pass", Commit: shaA, Attempt: 1, Seconds: 7}
	verified := Line{TS: "t3", Slice: "a", Event: EventVerified, Commit: shaA, Attempt: 1}
	failed := Line{TS: "t2", Slice: "a", Event: EventOracle, Outcome: "fail", Commit: shaA, Attempt: 1, Seconds: 3, Summary: "FAIL TestClamp"}
	errored := Line{TS: "t2", Slice: "a", Event: EventVerifyError, Commit: shaA, Attempt: 1, Summary: "no worktree"}

	for _, c := range []struct {
		name  string
		lines []Line
		sha   string
		slice string
		want  Verification
	}{
		{"no lines", nil, shaA, "a", Verification{}},
		{"an empty sha matches nothing", []Line{{Slice: "a", Event: EventVerifyPending}}, "", "a", Verification{}},
		{"pending alone is no result", []Line{pending}, shaA, "a", Verification{}},
		{"pending then a pass", []Line{pending, passed}, shaA, "a", Verification{State: VerifyPass, Attempt: 1, TS: "t2", Seconds: 7}},
		{"the verified line keeps the oracle line's time", []Line{pending, passed, verified}, shaA, "a", Verification{State: VerifyPass, Attempt: 1, TS: "t3", Seconds: 7}},
		{"a verified line alone is a pass", []Line{verified}, shaA, "a", Verification{State: VerifyPass, Attempt: 1, TS: "t3"}},
		{"a fail carries its summary", []Line{pending, failed}, shaA, "a", Verification{State: VerifyFail, Attempt: 1, TS: "t2", Seconds: 3, Summary: "FAIL TestClamp"}},
		{"an error carries its reason", []Line{pending, errored}, shaA, "a", Verification{State: VerifyError, Attempt: 1, TS: "t2", Summary: "no worktree"}},
		{"a rerun's pending line hides no earlier outcome", []Line{pending, failed, {TS: "t9", Slice: "a", Event: EventVerifyPending, Commit: shaA, Attempt: 2}}, shaA, "a",
			Verification{State: VerifyFail, Attempt: 1, TS: "t2", Seconds: 3, Summary: "FAIL TestClamp"}},
		{"another commit is another verification", []Line{pending, passed}, shaB, "a", Verification{}},
		{"another slice is another verification", []Line{pending, passed}, shaA, "b", Verification{}},
		{"a recorded line says nothing", []Line{passed, {Slice: "a", Event: EventRecorded, Commit: shaA, Attempt: 1}}, shaA, "a", Verification{State: VerifyPass, Attempt: 1, TS: "t2", Seconds: 7}},
		{"an oracle line with no outcome says nothing", []Line{passed, {Slice: "a", Event: EventOracle, Commit: shaA}}, shaA, "a", Verification{State: VerifyPass, Attempt: 1, TS: "t2", Seconds: 7}},
		{"a commit-less oracle line (a dirty tree) matches no commit", []Line{{Slice: "a", Event: EventOracle, Outcome: "pass"}}, shaA, "a", Verification{}},
	} {
		if got := VerificationState(c.lines, c.sha, c.slice); got != c.want {
			t.Errorf("%s: VerificationState = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// TestAnOldPendingLineSaysNothingOfAVerification: a verify-pending line an old
// worker wrote is no result, whoever wrote it and whatever follows; it still
// parses as a line, but no reader of verification state sees it.
func TestAnOldPendingLineSaysNothingOfAVerification(t *testing.T) {
	t.Parallel()
	pending := Line{TS: "1", Slice: "a", Event: EventVerifyPending, Commit: shaA, Attempt: 1, Session: "s", Summary: "cart: fix Total"}
	if v := VerificationState([]Line{pending}, shaA, "a"); v.State != VerifyNone {
		t.Errorf("a pending line alone = %+v, want no verification", v)
	}
	errored := Line{TS: "2", Slice: "a", Event: EventVerifyError, Commit: shaA, Attempt: 1, Summary: "no worktree"}
	if v := VerificationState([]Line{pending, errored, {TS: "3", Slice: "a", Event: EventVerifyPending, Commit: shaA, Attempt: 2}}, shaA, "a"); v.State != VerifyError || v.Attempt != 1 {
		t.Errorf("an error then a pending line = %+v, want the error", v)
	}
}

// TestNextAttempt numbers a slice's verifications as one sequence with the
// frontier's attempts, whatever kind of line carries the number.
func TestNextAttempt(t *testing.T) {
	t.Parallel()
	lines := []Line{
		{Slice: "a", Event: EventDispatch, Attempt: 1},
		{Slice: "a", Event: EventOracle, Outcome: "fail", Attempt: 1},
		{Slice: "a", Event: EventDispatch, Attempt: 2},
		{Slice: "b", Event: EventVerifyPending, Attempt: 4},
		{Slice: "", Event: EventGateOpen, Attempt: 9},
	}
	for slice, want := range map[string]int{"a": 3, "b": 5, "c": 1, "": 10} {
		if got := NextAttempt(lines, slice); got != want {
			t.Errorf("NextAttempt(%q) = %d, want %d", slice, got, want)
		}
	}
	if got := NextAttempt(nil, "a"); got != 1 {
		t.Errorf("NextAttempt on an empty journal = %d, want 1", got)
	}
}

// TestNextAttemptIgnoresFixSliceLines keeps a fix slice's first verification
// attempt 1: the gate's fix-slice line is not an attempt, and a journal that
// still carries the round number on it numbers right too.
func TestNextAttemptIgnoresFixSliceLines(t *testing.T) {
	t.Parallel()
	lines := []Line{
		{Event: EventGateRound, Attempt: 2},
		{Slice: "fix-1", Event: EventFixSlice, Attempt: 2},
		{Slice: "fix-2", Event: EventFixSlice},
	}
	for _, slice := range []string{"fix-1", "fix-2"} {
		if got := NextAttempt(lines, slice); got != 1 {
			t.Errorf("NextAttempt(%q) = %d, want 1", slice, got)
		}
	}
	lines = append(lines, Line{Slice: "fix-1", Event: EventDispatch, Attempt: 1})
	if got := NextAttempt(lines, "fix-1"); got != 2 {
		t.Errorf("NextAttempt after a first verification = %d, want 2", got)
	}
}

// TestVerificationStateSkipsAnUncoveredLine: a run that covers no commit is
// recorded and says nothing of the commit: it neither makes a failure nor a
// pass of it, and a covering line before or after it stands as if it were not
// there.
func TestVerificationStateSkipsAnUncoveredLine(t *testing.T) {
	const sha = "abc123"
	uncovered := Line{Event: EventOracle, Outcome: "pass", Slice: "a", Commit: sha, Attempt: 2, Uncovered: UncoveredDirtyBefore}
	if v := VerificationState([]Line{uncovered}, sha, "a"); v.State != VerifyNone {
		t.Errorf("an uncovered pass alone = %+v, want no verification", v)
	}
	fail := Line{Event: EventOracle, Outcome: "fail", Slice: "a", Commit: sha, Attempt: 3, Uncovered: UncoveredOtherCmd}
	covering := Line{Event: EventOracle, Outcome: "pass", Slice: "a", Commit: sha, Attempt: 1}
	if v := VerificationState([]Line{covering, fail}, sha, "a"); v.State != VerifyPass || v.Attempt != 1 {
		t.Errorf("a pass then an uncovered failure = %+v, want the pass", v)
	}
	if got := BuiltCommits([]Line{uncovered}); len(got) != 0 {
		t.Errorf("BuiltCommits of an uncovered run = %v, want none: no verified line is written for it", got)
	}
}
