package tracker_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/tracker"
)

func TestGraduation(t *testing.T) {
	st, cfg := newTestStore(t, localCfg())
	a, err := tracker.New(cfg, st)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	g := tracker.Graduation{
		Chart: "Do the thing",
		Tickets: []tracker.Draft{
			{Title: "Slice A", Body: "First slice"},
			{Title: "Slice B", Body: "Depends on A", BlockedBy: []tracker.Blocker{{Ref: "#1", Kind: "merged"}}},
			{Title: "Slice C", Body: "Depends on A and B", BlockedBy: []tracker.Blocker{{Ref: "#1", Kind: "merged"}, {Ref: "#2", Kind: "stacked"}}},
		},
	}

	ids, err := tracker.Graduate(a, st, g, nil)
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
		if _, err := os.Stat(st.TicketDir(id)); err != nil {
			t.Fatalf("ticket folder missing for %s: %v", id, err)
		}
	}

	// Each ticket's tracker/ticket.md must have the title and body.
	for i, id := range ids {
		ticketPath := filepath.Join(st.TicketDir(id), "tracker", "ticket.md")
		data, err := os.ReadFile(ticketPath)
		if err != nil {
			t.Fatalf("read ticket.md for %s: %v", id, err)
		}
		content := string(data)
		if !strings.Contains(content, g.Tickets[i].Title) {
			t.Fatalf("ticket.md for %s missing title %q, got %q", id, g.Tickets[i].Title, content)
		}
		if !strings.Contains(content, g.Tickets[i].Body) {
			t.Fatalf("ticket.md for %s missing body %q, got %q", id, g.Tickets[i].Body, content)
		}
	}

	// No subtasks.yaml should be created.
	for _, id := range ids {
		subtasksPath := filepath.Join(st.TicketDir(id), "tracker", "subtasks.yaml")
		if _, err := os.Stat(subtasksPath); err == nil {
			t.Fatalf("subtasks.yaml should not exist for %s", id)
		}
	}

	// Slice A has no blockers, so it gets no ticket.yaml at all.
	if _, err := os.Stat(filepath.Join(st.TicketDir(ids[0]), "ticket.yaml")); !os.IsNotExist(err) {
		t.Fatalf("ticket.yaml for %s: want absent, stat err = %v", ids[0], err)
	}

	// Slice B and C get ticket.yaml with resolved ids and explicit kinds.
	depsB, err := st.ReadTicketDeps(ids[1])
	if err != nil {
		t.Fatalf("ReadTicketDeps(%s): %v", ids[1], err)
	}
	wantB := []store.TicketBlockedBy{{Ticket: ids[0], Kind: "merged"}}
	if !reflect.DeepEqual(depsB, wantB) {
		t.Fatalf("ticket.yaml for %s = %+v, want %+v", ids[1], depsB, wantB)
	}

	depsC, err := st.ReadTicketDeps(ids[2])
	if err != nil {
		t.Fatalf("ReadTicketDeps(%s): %v", ids[2], err)
	}
	wantC := []store.TicketBlockedBy{{Ticket: ids[0], Kind: "merged"}, {Ticket: ids[1], Kind: "stacked"}}
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
	st, cfg := newTestStore(t, localCfg())
	a, err := tracker.New(cfg, st)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	g := tracker.Graduation{
		Tickets: []tracker.Draft{
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

	ids, err := tracker.Graduate(a, st, g, onMinted)
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
	st, cfg := newTestStore(t, localCfg())
	a, err := tracker.New(cfg, st)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	g := tracker.Graduation{
		Tickets: []tracker.Draft{
			{Title: "Slice A", BlockedBy: []tracker.Blocker{{Ref: "#2", Kind: "merged"}}},
			{Title: "Slice B"},
		},
	}

	ids, err := tracker.Graduate(a, st, g, nil)
	if err == nil {
		t.Fatalf("Graduate: want an error for an unresolved \"#2\" ref, got ids %v", ids)
	}
	if ids[0] != "" {
		t.Fatalf("ids[0] = %q, want empty: the ticket referencing an unresolved ref must never be minted", ids[0])
	}
}
