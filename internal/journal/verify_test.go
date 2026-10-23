package journal

import (
	"reflect"
	"testing"
)

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// TestVerificationState reads the latest record of one (commit, slice) from
// the lines that mention it: the digester's verify-pending and verify-error
// lines and the frontier's oracle and verified lines alike, the last in
// journal order winning.
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
		{"pending", []Line{pending}, shaA, "a", Verification{State: VerifyPending, Attempt: 1, TS: "t1", Session: "s1"}},
		{"pending then a pass", []Line{pending, passed}, shaA, "a", Verification{State: VerifyPass, Attempt: 1, TS: "t2", Seconds: 7}},
		{"the verified line keeps the oracle line's time", []Line{pending, passed, verified}, shaA, "a", Verification{State: VerifyPass, Attempt: 1, TS: "t3", Seconds: 7}},
		{"a verified line alone is a pass", []Line{verified}, shaA, "a", Verification{State: VerifyPass, Attempt: 1, TS: "t3"}},
		{"a fail carries its summary", []Line{pending, failed}, shaA, "a", Verification{State: VerifyFail, Attempt: 1, TS: "t2", Seconds: 3, Summary: "FAIL TestClamp"}},
		{"an error carries its reason", []Line{pending, errored}, shaA, "a", Verification{State: VerifyError, Attempt: 1, TS: "t2", Summary: "no worktree"}},
		{"a rerun replaces the outcome with its pending", []Line{pending, failed, {TS: "t9", Slice: "a", Event: EventVerifyPending, Commit: shaA, Attempt: 2}}, shaA, "a",
			Verification{State: VerifyPending, Attempt: 2, TS: "t9"}},
		{"another commit is another verification", []Line{pending, passed}, shaB, "a", Verification{}},
		{"another slice is another verification", []Line{pending, passed}, shaA, "b", Verification{}},
		{"a recorded line says nothing", []Line{pending, {Slice: "a", Event: EventRecorded, Commit: shaA, Attempt: 1}}, shaA, "a", Verification{State: VerifyPending, Attempt: 1, TS: "t1", Session: "s1"}},
		{"an oracle line with no outcome says nothing", []Line{pending, {Slice: "a", Event: EventOracle, Commit: shaA}}, shaA, "a", Verification{State: VerifyPending, Attempt: 1, TS: "t1", Session: "s1"}},
		{"a commit-less oracle line (a dirty tree) matches no commit", []Line{{Slice: "a", Event: EventOracle, Outcome: "pass"}}, shaA, "a", Verification{}},
	} {
		if got := VerificationState(c.lines, c.sha, c.slice); got != c.want {
			t.Errorf("%s: VerificationState = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// TestPending lists the verifications whose latest record is a pending line,
// in the order the lines were written, and not the ones a later line settled.
func TestPending(t *testing.T) {
	t.Parallel()
	lines := []Line{
		{TS: "1", Slice: "a", Event: EventVerifyPending, Commit: shaA, Attempt: 1},
		{TS: "2", Slice: "b", Event: EventVerifyPending, Commit: shaA, Attempt: 1, Session: "s"},
		{TS: "3", Slice: "a", Event: EventOracle, Outcome: "pass", Commit: shaA, Attempt: 1},
		{TS: "4", Slice: "a", Event: EventVerifyPending, Commit: shaB, Attempt: 2},
		{TS: "5", Slice: "c", Event: EventVerifyPending, Commit: shaA, Attempt: 1},
		{TS: "6", Slice: "c", Event: EventVerifyError, Commit: shaA, Attempt: 1},
		{TS: "7", Slice: "b", Event: EventVerifyPending, Commit: shaA, Attempt: 2},
		{TS: "8", Slice: "d", Event: EventVerifyPending, Attempt: 1}, // no commit: not a verification of one
	}
	want := []PendingVerification{
		{Slice: "a", Commit: shaB, Attempt: 2, TS: "4"},
		{Slice: "b", Commit: shaA, Attempt: 2, TS: "7"},
	}
	if got := Pending(lines); !reflect.DeepEqual(got, want) {
		t.Errorf("Pending = %+v, want %+v", got, want)
	}
	if got := Pending(nil); len(got) != 0 {
		t.Errorf("Pending(nil) = %+v, want none", got)
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
