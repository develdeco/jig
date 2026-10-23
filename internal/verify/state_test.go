package verify

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// TestAPassStoppedBeforeItsVerifiedLineIsFinished: a run that stopped between
// marking the slice green and writing the verified line leaves a green slice
// and an oracle pass line with no verified line. The next jig verify of the
// commit writes that line, once, with the pass's attempt and session; a slice
// that is not green is left alone.
func TestAPassStoppedBeforeItsVerifiedLineIsFinished(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	if res, err := Run(r.deps, r.request("a", sha)); err != nil || res.State != journal.VerifyPass {
		t.Fatalf("Run = %+v, %v, want a pass", res, err)
	}
	// The run that stopped: the same journal without its verified line.
	path := filepath.Join(r.st.TicketDir(r.fx.Ticket), "journal.ndjson")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.SplitAfter(string(body), "\n") {
		if !strings.Contains(line, `"event":"verified"`) {
			kept = append(kept, line)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(kept, "")), 0o600); err != nil {
		t.Fatal(err)
	}
	verifiedLines := func() []journal.Line {
		var out []journal.Line
		for _, l := range r.lines() {
			if l.Event == journal.EventVerified && l.Commit == sha && l.Slice == "a" {
				out = append(out, l)
			}
		}
		return out
	}

	// A slice that is not green is not this case: nothing is written.
	if err := r.st.WriteSliceState(r.fx.Ticket, "a", store.SliceState{State: "building", Attempts: 1}); err != nil {
		t.Fatal(err)
	}
	if res, err := Run(r.deps, Request{Ticket: r.fx.Ticket, Slice: "a", SHA: sha, Session: "s-2"}); err != nil || !res.Existing {
		t.Fatalf("Run on a building slice = %+v, %v, want the existing pass", res, err)
	}
	if got := verifiedLines(); len(got) != 0 {
		t.Fatalf("verified lines with the slice building = %+v, want none", got)
	}

	// The green slice: the next run finishes the pass, once.
	if err := r.st.WriteSliceState(r.fx.Ticket, "a", store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if res, err := Run(r.deps, Request{Ticket: r.fx.Ticket, Slice: "a", SHA: sha, Session: "s-2"}); err != nil || !res.Existing || res.State != journal.VerifyPass {
			t.Fatalf("Run %d on the stopped pass = %+v, %v, want the existing pass", i, res, err)
		}
	}
	got := verifiedLines()
	if len(got) != 1 || got[0].Attempt != 1 || got[0].Session != "s-1" || got[0].Agent != "agent-1" {
		t.Errorf("verified lines = %+v, want one, of attempt 1 by s-1 and agent-1 (the pass's)", got)
	}
	if r.state("a").State != "green" {
		t.Errorf("slice a = %+v, want it still green", r.state("a"))
	}
}
