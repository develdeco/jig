package correct

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

func oracleFail(slice, sig string, attempt int) journal.Line {
	return journal.Line{Slice: slice, Event: journal.EventOracle, Outcome: "fail", Signature: sig, Attempt: attempt, Commit: "sha" + sig, Summary: "FAIL " + sig,
		TS: fmt.Sprintf("2026-10-01T10:00:%02dZ", attempt)}
}

func TestStallsNeedTwoFailuresWithOneSignature(t *testing.T) {
	t.Parallel()
	if got := Stalls([]journal.Line{oracleFail("a", "x", 1)}); got != nil {
		t.Errorf("one failure = %+v, want no stall", got)
	}
	if got := Stalls([]journal.Line{oracleFail("a", "x", 1), oracleFail("a", "y", 2)}); got != nil {
		t.Errorf("two failures of different signatures = %+v, want no stall", got)
	}
	got := Stalls([]journal.Line{oracleFail("a", "x", 1), oracleFail("a", "y", 2), oracleFail("a", "x", 3)})
	want := []Stall{{Slice: "a", Signature: "x", Count: 2, Commit: "shax", Attempt: 3, TS: "2026-10-01T10:00:03Z", Summary: "FAIL x"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("a signature that came back after another = %+v, want %+v", got, want)
	}
}

func TestStallsAreCountedPerSliceAndOrderedByTheirLatestFailure(t *testing.T) {
	t.Parallel()
	lines := []journal.Line{
		oracleFail("a", "x", 1), oracleFail("b", "x", 1), // the same signature on two slices is no repeat
		oracleFail("b", "x", 2), oracleFail("a", "x", 2),
		oracleFail("a", "x", 3),
	}
	got := Stalls(lines)
	if len(got) != 2 || got[0].Slice != "b" || got[0].Count != 2 || got[1].Slice != "a" || got[1].Count != 3 {
		t.Errorf("Stalls = %+v, want b (2) then a (3)", got)
	}
}

func TestStallsIgnoreWhatIsNoSignedFailure(t *testing.T) {
	t.Parallel()
	pass := oracleFail("a", "x", 1)
	pass.Outcome = "pass"
	unsigned := oracleFail("a", "", 2)
	other := oracleFail("a", "x", 3)
	other.Event = journal.EventVerifyError
	lines := []journal.Line{pass, pass, unsigned, unsigned, other, other}
	if got := Stalls(lines); got != nil {
		t.Errorf("passes, unsigned failures and other events = %+v, want no stall", got)
	}
}

func TestStallsStartOverWhenTheSliceVerifiesOrIsRequeued(t *testing.T) {
	t.Parallel()
	verified := journal.Line{Slice: "a", Event: journal.EventVerified}
	requeue := journal.Line{Slice: "a", Event: journal.EventRequeue}
	otherSlice := journal.Line{Slice: "b", Event: journal.EventVerified}

	if got := Stalls([]journal.Line{oracleFail("a", "x", 1), oracleFail("a", "x", 2), verified}); got != nil {
		t.Errorf("failures a later pass followed = %+v, want none", got)
	}
	if got := Stalls([]journal.Line{oracleFail("a", "x", 1), verified, oracleFail("a", "x", 2)}); got != nil {
		t.Errorf("one failure on each side of a pass = %+v, want none", got)
	}
	if got := Stalls([]journal.Line{oracleFail("a", "x", 1), oracleFail("a", "x", 2), requeue, oracleFail("a", "x", 3)}); got != nil {
		t.Errorf("a failure after a requeue = %+v, want none: the slice started over", got)
	}
	got := Stalls([]journal.Line{oracleFail("a", "x", 1), oracleFail("a", "x", 2), requeue, oracleFail("a", "x", 3), otherSlice, oracleFail("a", "x", 4)})
	if len(got) != 1 || got[0].Count != 2 || got[0].Attempt != 4 {
		t.Errorf("two failures after a requeue, and a pass of another slice between = %+v, want one stall of count 2 at attempt 4", got)
	}
}

func TestStallRefNamesTheRepeatNotJustTheFailure(t *testing.T) {
	t.Parallel()
	one := Stalls([]journal.Line{oracleFail("a", "x", 1), oracleFail("a", "x", 2)})[0]
	more := Stalls([]journal.Line{oracleFail("a", "x", 1), oracleFail("a", "x", 2), oracleFail("a", "x", 3)})[0]
	again := Stalls([]journal.Line{oracleFail("a", "x", 1), oracleFail("a", "x", 2)})[0]
	if one.Ref() != again.Ref() {
		t.Errorf("the same stall has Refs %q and %q, want one", one.Ref(), again.Ref())
	}
	if one.Ref() == more.Ref() {
		t.Errorf("a third failure has the Ref %q of the second: a session that went on failing is not told again", one.Ref())
	}
	if !strings.HasPrefix(one.Ref(), "stall:a:") {
		t.Errorf("Ref = %q, want it to name the slice", one.Ref())
	}
}

func TestStalledMovesOnlyASliceThatIsBeingWorked(t *testing.T) {
	t.Parallel()
	s := Stall{Slice: "a", Signature: "sig", Summary: "why", Attempt: 3}
	for _, c := range []struct {
		from    store.SliceState
		changed bool
	}{
		{store.SliceState{State: "queued", Attempts: 1}, true},
		{store.SliceState{State: "building", Attempts: 5, Question: "q-001", Reason: "old"}, true},
		{store.SliceState{State: ""}, true},
		{store.SliceState{State: "stalled", Reason: "attempt-cap", Attempts: 3}, false},
		{store.SliceState{State: "green", Attempts: 1}, false},
		{store.SliceState{State: "needs-input", Question: "q-001"}, false},
		{store.SliceState{State: "env-blocked", Reason: "env-up-failed"}, false},
	} {
		got, changed := Stalled(c.from, s)
		if changed != c.changed {
			t.Errorf("Stalled(%s) changed = %v, want %v", c.from.State, changed, c.changed)
		}
		if !changed {
			if got != c.from {
				t.Errorf("Stalled(%s) = %+v, want the state untouched", c.from.State, got)
			}
			continue
		}
		if got.State != "stalled" || got.Reason != "stall" || got.Signature != "sig" || got.StallSummary != "why" || got.Attempts != max(c.from.Attempts, 3) {
			t.Errorf("Stalled(%s) = %+v, want stalled for a stall, with the signature, the summary and the attempts", c.from.State, got)
		}
	}
}
