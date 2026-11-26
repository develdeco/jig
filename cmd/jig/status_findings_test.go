package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// TestStatusSlicesNameTheFindingsAFixSliceAnswers: the slices table ends in a
// findings column, the finding ids a slice resolves joined by "+" like
// blocked_by, and "-" for a slice that answers none, so a reader pairs a fix
// slice with its findings by id rather than by the text of its goal.
func TestStatusSlicesNameTheFindingsAFixSliceAnswers(t *testing.T) {
	t.Parallel()
	fx := newStoreOnlyFixture(t)
	st := openStore(t, fx)
	fix := store.Slice{ID: "fix-1", Workspace: "alpha", Goal: "Fix the findings", Oracle: "go test ./...", FromGate: 2, Findings: []string{"r1-f1", "r1-f2"}}
	if err := st.AppendSlices(fx.Ticket, []store.Slice{fix}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteSliceState(fx.Ticket, "fix-1", store.SliceState{State: "queued"}); err != nil {
		t.Fatal(err)
	}
	got := block(statusOf(t, fx, st), "slices[")
	if len(got) < 2 || !strings.HasSuffix(got[0], ",from_gate,findings}:") {
		t.Fatalf("slices header = %q, want it to end in from_gate,findings", got)
	}
	if !strings.HasSuffix(got[1], ",-,-") {
		t.Errorf("an ordinary slice's row = %q, want it to end in a dash for its round and a dash for its findings", got[1])
	}
	last := got[len(got)-1]
	if !strings.HasPrefix(last, "  fix-1,") || !strings.HasSuffix(last, ",2,r1-f1+r1-f2") {
		t.Errorf("the fix slice's row = %q, want it to end in its round and its two finding ids joined by +", last)
	}
}
