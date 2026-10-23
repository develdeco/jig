package verify

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// TestRunRecordsPendingBeforeItDoesAnything pins "pending first": when the
// oracle starts, the pending line is already in the journal and the slice is
// not yet green.
func TestRunRecordsPendingBeforeItDoesAnything(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	var atOracle []string
	var stateAtOracle string
	r.deps.Oracle = func(cmd, dir string, env []string, limit time.Duration) (string, error) {
		atOracle = events(r.lines())
		stateAtOracle = r.state("a").State
		return "", nil
	}
	if _, err := Run(r.deps, r.request("a", sha)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := []string{"verify-pending"}; !reflect.DeepEqual(atOracle, want) {
		t.Errorf("journal when the oracle started = %v, want %v", atOracle, want)
	}
	if stateAtOracle != "queued" {
		t.Errorf("slice state when the oracle started = %q, want it untouched (queued)", stateAtOracle)
	}
}

// TestRunMovesTheSliceStateAsTheDesignSays: a pass is green from any state,
// the stalled one included, and clears what the stall left; a failure makes it
// building, a green slice whose newer commit fails goes back to building; a
// slice that waits on an answer keeps waiting.
func TestRunMovesTheSliceStateAsTheDesignSays(t *testing.T) {
	t.Parallel()
	stalled := store.SliceState{State: "stalled", Attempts: 3, Reason: "stall", Signature: "b|code-bug|x", StallSummary: "same failure twice"}
	cases := []struct {
		name  string
		from  store.SliceState
		fails bool
		want  store.SliceState
	}{
		{"queued passes to green", store.SliceState{State: "queued"}, false, store.SliceState{State: "green", Attempts: 1}},
		{"building passes to green", store.SliceState{State: "building", Attempts: 1}, false, store.SliceState{State: "green", Attempts: 2}},
		{"stalled passes to green and forgets the stall", stalled, false, store.SliceState{State: "green", Attempts: 4}},
		{"needs-input passes to green", store.SliceState{State: "needs-input", Attempts: 1, Question: "q-001"}, false, store.SliceState{State: "green", Attempts: 2}},
		{"queued fails to building", store.SliceState{State: "queued"}, true, store.SliceState{State: "building", Attempts: 1}},
		{"green fails back to building", store.SliceState{State: "green", Attempts: 1}, true, store.SliceState{State: "building", Attempts: 2}},
		{"stalled fails to building and forgets the stall", stalled, true, store.SliceState{State: "building", Attempts: 4}},
		{"needs-input stays waiting on its answer", store.SliceState{State: "needs-input", Attempts: 1, Question: "q-001"}, true, store.SliceState{State: "needs-input", Attempts: 2, Question: "q-001"}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, fixture.Opts{})
			sha := r.fixClamp()
			if c.fails {
				r.oracle.err = errExit
				r.oracle.out = "FAIL"
			}
			if err := r.st.WriteSliceState(r.fx.Ticket, "a", c.from); err != nil {
				t.Fatal(err)
			}
			// Earlier attempts the state counts are in the journal too, as the
			// frontier or earlier verifications wrote them.
			for i := 1; i <= c.from.Attempts; i++ {
				if err := journal.Append(r.st, r.fx.Ticket, journal.Line{Slice: "a", Event: journal.EventDispatch, Attempt: i}); err != nil {
					t.Fatal(err)
				}
			}
			res, err := Run(r.deps, r.request("a", sha))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if res.Attempt != c.from.Attempts+1 {
				t.Errorf("attempt = %d, want %d: one past the slice's earlier attempts", res.Attempt, c.from.Attempts+1)
			}
			// The slice keeps counting from the journal's numbering: the
			// verification is its next attempt.
			got := r.state("a")
			if got != c.want {
				t.Errorf("state = %+v, want %+v", got, c.want)
			}
		})
	}
}

// TestAnOlderCommitFinishingLastLeavesTheNewerOnesState: workers finish in
// any order. When the run of an older commit of a slice ends after a newer
// commit's, the slice keeps the state the newer one gave it, pass or fail.
func TestAnOlderCommitFinishingLastLeavesTheNewerOnesState(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		newerFails bool
		olderFails bool
		want       string
	}{
		{"an older failure after a newer pass leaves it green", false, true, "green"},
		{"an older pass after a newer failure leaves it building", true, false, "building"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, fixture.Opts{})
			older := r.fixClamp()
			newer := r.commit("a", "a: and a note", map[string]string{"alpha/NOTE.txt": "x\n"})
			setOutcome := func(fail bool) {
				r.oracle.err, r.oracle.out = nil, ""
				if fail {
					r.oracle.err, r.oracle.out = errExit, "FAIL"
				}
			}
			setOutcome(c.newerFails)
			if _, err := Run(r.deps, r.request("a", newer)); err != nil {
				t.Fatalf("Run newer: %v", err)
			}
			setOutcome(c.olderFails)
			if _, err := Run(r.deps, r.request("a", older)); err != nil {
				t.Fatalf("Run older: %v", err)
			}
			got := r.state("a")
			if got.State != c.want {
				t.Errorf("state after the older commit finished last = %q, want %q (the newer commit's)", got.State, c.want)
			}
			if got.Attempts != 2 {
				t.Errorf("attempts = %d, want 2: both runs count", got.Attempts)
			}
		})
	}
}

// TestTheSliceIsGreenBeforeTheVerifiedLineSaysSo pins the order of a pass's
// last two writes: a reader that the verified line wakes (jig events, the
// TUI, a hook) reads the slice's state next, so the state must already be
// green when that line is written. The clock is read to stamp each line, so
// a clock that looks at the slice state at every reading sees what a reader
// woken by the line would.
func TestTheSliceIsGreenBeforeTheVerifiedLineSaysSo(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	var mu sync.Mutex
	stateAt := map[time.Time]string{}
	d := r.deps
	d.Now = func() time.Time {
		r.clock.advance(time.Second)
		now := r.clock.Now().UTC()
		st, err := r.st.ReadSliceState(r.fx.Ticket, "a")
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			stateAt[now] = "unreadable: " + err.Error()
		} else {
			stateAt[now] = st.State
		}
		return now
	}
	if res, err := Run(d, r.request("a", sha)); err != nil || res.State != journal.VerifyPass {
		t.Fatalf("Run = %+v, %v, want a pass", res, err)
	}
	var verified *journal.Line
	for _, l := range r.lines() {
		if l.Event == journal.EventVerified && l.Slice == "a" && l.Commit == sha {
			verified = &l
		}
	}
	if verified == nil {
		t.Fatalf("no verified line for %s: %v", sha, events(r.lines()))
	}
	at, err := time.Parse(time.RFC3339Nano, verified.TS)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got, ok := stateAt[at.UTC()]; !ok || got != "green" {
		t.Errorf("slice a when its verified line was written = %q (stamped %v), want green", got, ok)
	}
}
