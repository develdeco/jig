package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/store"
)

// stateLine is the word of the state: line of a status.
func stateLine(t *testing.T, st *store.Store, ticket string) string {
	t.Helper()
	out, err := RenderStatus(st, ticket)
	if err != nil {
		t.Fatalf("RenderStatus(%s): %v", ticket, err)
	}
	for _, l := range strings.Split(out, "\n") {
		if w, ok := strings.CutPrefix(l, "state: "); ok {
			return w
		}
	}
	t.Fatalf("status of %s has no state line:\n%s", ticket, out)
	return ""
}

// TestStatusStateOfATicketNothingHasStarted: the state: line is new for a
// ticket with no slices and no adopted branch, queued for one whose slices
// nobody ever claimed or verified, and building once a slice was claimed or
// has a verification (or an attempt, as the dispatch era recorded it), even if
// it is queued again now. The states that rank above them are unchanged, and
// the answer agrees with the phase the ticket is in: new is still the brief,
// queued has the build ahead, building is in it.
func TestStatusStateOfATicketNothingHasStarted(t *testing.T) {
	t.Parallel()
	fx := newStoreOnlyFixture(t)
	st := openStore(t, fx)

	two := []store.Slice{{ID: "x", Workspace: "alpha", Goal: "Build x.", Oracle: "test"}, {ID: "y", Workspace: "alpha", Goal: "Build y.", Oracle: "test"}}
	const (
		noBrief  = ""
		drafted  = "drafted"
		approved = "approved"
	)
	// ticket makes the next ticket of the store with slices (none when nil),
	// a brief in the given state, and the journal lines.
	n := 1
	ticket := func(slices []store.Slice, brief string, lines ...journal.Line) string {
		t.Helper()
		n++
		id := fmt.Sprintf("JIG-%d", n)
		if err := st.CreateTicketRecord(id, store.Ticket{Title: "Ticket " + id}); err != nil {
			t.Fatalf("CreateTicketRecord(%s): %v", id, err)
		}
		if len(slices) > 0 {
			if err := st.AppendSlices(id, slices); err != nil {
				t.Fatalf("AppendSlices(%s): %v", id, err)
			}
		}
		if brief != noBrief {
			writeBrief(t, st, id)
		}
		if brief == approved {
			approveBrief(t, st, id)
		}
		for _, l := range lines {
			if err := journal.Append(st, id, l); err != nil {
				t.Fatalf("journal.Append(%s): %v", id, err)
			}
		}
		return id
	}
	claim := journal.Line{Event: journal.EventClaim, Slice: "x", Session: "s-1"}
	release := journal.Line{Event: journal.EventRelease, Slice: "x", Session: "s-1"}
	approval := journal.Line{Event: journal.EventApproval, Provenance: "person"}
	pending := journal.Line{Event: journal.EventVerifyPending, Slice: "x", Commit: "abcdef0123"}

	tests := []struct {
		name    string
		id      string
		want    string
		current phase.Name
	}{
		{"no slices and no branch is new", ticket(nil, noBrief), "new", phase.Brief},
		{"slices nobody started are queued", ticket(two, approved, approval), "queued", phase.Build},
		{"queued, the brief not yet approved", ticket(two, drafted), "queued", phase.Brief},
		{"a claim is building", ticket(two, approved, approval, claim), "building", phase.Build},
		{"a slice released after its claim is still building", ticket(two, approved, approval, claim, release), "building", phase.Build},
		{"a verification is building", ticket(two, approved, approval, pending), "building", phase.Build},
	}
	for _, tc := range tests {
		if got := stateLine(t, st, tc.id); got != tc.want {
			t.Errorf("%s: state = %s, want %s", tc.name, got, tc.want)
		}
		p, err := derivePhase(st, tc.id)
		if err != nil {
			t.Fatalf("%s: derivePhase: %v", tc.name, err)
		}
		if p.Current != tc.current {
			t.Errorf("%s: the phase is %s, want %s", tc.name, p.Current, tc.current)
		}
	}

	// An attempt on a queued slice (the dispatch era's record of a claim) is
	// building too.
	id := ticket(two, approved, approval)
	if err := st.WriteSliceState(id, "x", store.SliceState{State: "queued", Attempts: 1}); err != nil {
		t.Fatal(err)
	}
	if got := stateLine(t, st, id); got != "building" {
		t.Errorf("a queued slice with an attempt: state = %s, want building", got)
	}

	// A slice past queued is building without a journal line, and a worse
	// state still outranks it.
	for _, tc := range []struct{ slice, want string }{
		{"building", "building"}, {"green", "building"}, {"stalled", "stalled"}, {"needs-input", "parked"}, {"env-blocked", "env-blocked"},
	} {
		id := ticket(two, approved, approval)
		if err := st.WriteSliceState(id, "x", store.SliceState{State: tc.slice}); err != nil {
			t.Fatal(err)
		}
		if got := stateLine(t, st, id); got != tc.want {
			t.Errorf("slice x %s, y queued: state = %s, want %s", tc.slice, got, tc.want)
		}
	}

	// A ticket that adopted a branch has no slices and reads green, not new.
	id = ticket(nil, noBrief)
	if err := st.WriteTicketBranch(id, "add-retry"); err != nil {
		t.Fatal(err)
	}
	if got := stateLine(t, st, id); got != "green" {
		t.Errorf("an adopted ticket with no slices: state = %s, want green", got)
	}
}
