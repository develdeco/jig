package store

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestGraduation(t *testing.T) {
	st := &Store{Root: t.TempDir()}

	g := Graduation{
		Chart: "Do the thing",
		Tickets: []Draft{
			{Title: "Slice A", Body: "First slice"},
			{Title: "Slice B", Body: "Depends on A", BlockedBy: []Blocker{{Ref: "#1", Kind: "merged"}}},
			{Title: "Slice C", Body: "Depends on A and B", BlockedBy: []Blocker{{Ref: "#1", Kind: "merged"}, {Ref: "#2", Kind: "stacked"}}},
		},
	}

	ids, err := st.Graduate("JIG-{n}", g, nil)
	if err != nil {
		t.Fatalf("Graduate: %v", err)
	}
	want := []string{"JIG-1", "JIG-2", "JIG-3"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i, id := range ids {
		if id != want[i] {
			t.Fatalf("ids[%d] = %q, want %q", i, id, want[i])
		}
	}

	// Slice A has no blockers, so its ticket.yaml records a title and no
	// blocked_by.
	gotA, err := st.ReadTicket(ids[0])
	if err != nil {
		t.Fatalf("ReadTicket(%s): %v", ids[0], err)
	}
	if gotA.Title != g.Tickets[0].Title || len(gotA.BlockedBy) != 0 {
		t.Fatalf("ticket.yaml for %s = %+v, want title %q and no blockers", ids[0], gotA, g.Tickets[0].Title)
	}

	// Slice B and C get ticket.yaml with resolved ids and explicit kinds.
	depsB, err := st.ReadTicketDeps(ids[1])
	if err != nil {
		t.Fatalf("ReadTicketDeps(%s): %v", ids[1], err)
	}
	wantB := []TicketBlockedBy{{Ticket: ids[0], Kind: "merged"}}
	if !reflect.DeepEqual(depsB, wantB) {
		t.Fatalf("ticket.yaml for %s = %+v, want %+v", ids[1], depsB, wantB)
	}

	depsC, err := st.ReadTicketDeps(ids[2])
	if err != nil {
		t.Fatalf("ReadTicketDeps(%s): %v", ids[2], err)
	}
	wantC := []TicketBlockedBy{{Ticket: ids[0], Kind: "merged"}, {Ticket: ids[1], Kind: "stacked"}}
	if !reflect.DeepEqual(depsC, wantC) {
		t.Fatalf("ticket.yaml for %s = %+v, want %+v", ids[2], depsC, wantC)
	}
}

// TestGraduateCallsOnMintedBeforeNextTicket covers the write-back contract a
// caller relies on: onMinted fires for ticket i, with its minted id, before
// ticket i+1 is minted - so a caller can persist ticket i's id and, if
// onMinted itself errors (standing in for "the caller's own write failed"),
// no further ticket is minted.
func TestGraduateCallsOnMintedBeforeNextTicket(t *testing.T) {
	st := &Store{Root: t.TempDir()}

	g := Graduation{
		Tickets: []Draft{
			{Title: "Slice A"},
			{Title: "Slice B"},
			{Title: "Slice C"},
		},
	}

	var got []struct {
		i  int
		id string
	}
	failAt := 1
	onMinted := func(i int, id string) error {
		got = append(got, struct {
			i  int
			id string
		}{i, id})
		if i == failAt {
			return fmt.Errorf("boom")
		}
		return nil
	}

	ids, err := st.Graduate("JIG-{n}", g, onMinted)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Graduate error = %v, want it to wrap \"boom\"", err)
	}
	if len(got) != failAt+1 {
		t.Fatalf("onMinted calls = %+v, want exactly %d (stopping at the failing one)", got, failAt+1)
	}
	for i, call := range got {
		if call.i != i {
			t.Fatalf("onMinted call %d has i=%d, want %d", i, call.i, i)
		}
		if call.id == "" {
			t.Fatalf("onMinted call %d has an empty id", i)
		}
	}
	// The ticket at failAt was minted (and its id reported to onMinted)
	// before onMinted's own error stopped the run; the one after it was
	// never minted.
	if ids[failAt] == "" {
		t.Fatalf("ids[%d] = %q, want the id minted just before the failure", failAt, ids[failAt])
	}
	if ids[failAt+1] != "" {
		t.Fatalf("ids[%d] = %q, want empty: minting must stop once onMinted errors", failAt+1, ids[failAt+1])
	}
}

// TestGraduateRefusesUnresolvedIndexRef covers a "#k" BlockedBy ref that
// names no minted ticket (out of range, or a sibling not minted yet):
// Graduate must error rather than write the literal "#k" into the store as
// a ticket id.
func TestGraduateRefusesUnresolvedIndexRef(t *testing.T) {
	st := &Store{Root: t.TempDir()}

	g := Graduation{
		Tickets: []Draft{
			{Title: "Slice A", BlockedBy: []Blocker{{Ref: "#2", Kind: "merged"}}},
			{Title: "Slice B"},
		},
	}

	ids, err := st.Graduate("JIG-{n}", g, nil)
	if err == nil {
		t.Fatalf("Graduate: want an error for an unresolved \"#2\" ref, got ids %v", ids)
	}
	if ids[0] != "" {
		t.Fatalf("ids[0] = %q, want empty: the ticket referencing an unresolved ref must never be minted", ids[0])
	}
}

// TestGraduateStopsAtAnUnusableID covers a ticket_format whose every mint is
// unusable (a reserved lease suffix): Graduate refuses the first ticket
// before writing anything under it, and mints no further sibling.
func TestGraduateStopsAtAnUnusableID(t *testing.T) {
	st := &Store{Root: t.TempDir()}

	g := Graduation{Tickets: []Draft{{Title: "Slice A"}, {Title: "Slice B"}}}
	ids, err := st.Graduate("JIG-{n}-gate", g, nil)
	if err == nil {
		t.Fatalf("Graduate with ticket_format JIG-{n}-gate: want a refusal, got ids %v", ids)
	}
	if ids[0] != "" || ids[1] != "" {
		t.Fatalf("ids = %v, want both empty: nothing was minted", ids)
	}
}
