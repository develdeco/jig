package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// TestKeepsRankWritesTheTicketRecord: jig rank writes the rank into the
// ticket's ticket.yaml, commits that record alone and pushes it to the store's
// origin; the rest of the record is kept; a rank already held is left as it
// is; --clear removes the key; and a call that cannot name a rank writes
// nothing.
func TestKeepsRankWritesTheTicketRecord(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	blocked := []store.TicketBlockedBy{{Ticket: fx.Ticket, Kind: "stacked"}}
	if err := st.CreateTicketRecord("JIG-2", store.Ticket{Title: "Second", BlockedBy: blocked}); err != nil {
		t.Fatal(err)
	}
	head := func() string { return strings.TrimSpace(gitMust(t, fx.StoreDir, "rev-parse", "HEAD")) }
	subject := func() string { return strings.TrimSpace(gitMust(t, fx.StoreDir, "log", "-1", "--format=%s")) }
	rank := func(args ...string) string {
		t.Helper()
		out, code := runMain(t, e, "", append([]string{"rank"}, args...)...)
		if code != 0 {
			t.Fatalf("jig rank %v: exit %d\n%s", args, code, out)
		}
		return out
	}

	out := rank("JIG-2", "4")
	if !strings.Contains(out, "rank:\n  ticket: JIG-2\n  rank: 4\n  result: recorded\n") {
		t.Errorf("jig rank JIG-2 4 says:\n%s", out)
	}
	rec, err := st.ReadTicket("JIG-2")
	if err != nil || rec.Rank == nil || *rec.Rank != 4 {
		t.Fatalf("ticket JIG-2 after jig rank = %+v, %v, want rank 4", rec, err)
	}
	if rec.Title != "Second" || !reflect.DeepEqual(rec.BlockedBy, blocked) {
		t.Errorf("jig rank changed the rest of the record: %+v", rec)
	}
	if got := subject(); got != "JIG-2: rank 4" {
		t.Errorf("store commit = %q, want \"JIG-2: rank 4\"", got)
	}
	if files := strings.TrimSpace(gitMust(t, fx.StoreDir, "show", "--name-only", "--format=", "HEAD")); files != "tickets/JIG-2/ticket.yaml" {
		t.Errorf("the rank commit holds %q, want tickets/JIG-2/ticket.yaml alone", files)
	}
	if origin := strings.TrimSpace(gitMust(t, fx.StoreRemote, "rev-parse", "HEAD")); origin != head() {
		t.Errorf("the origin is at %s, want the rank commit %s", origin, head())
	}

	// The same rank again is not a change: no commit.
	before := head()
	if out := rank("JIG-2", "4"); !strings.Contains(out, "result: unchanged") || head() != before {
		t.Errorf("jig rank JIG-2 4 again: head moved = %v, says:\n%s", head() != before, out)
	}

	// --clear removes the key and keeps the rest.
	out = rank("JIG-2", "--clear")
	if !strings.Contains(out, "rank: none") || !strings.Contains(out, "result: recorded") {
		t.Errorf("jig rank JIG-2 --clear says:\n%s", out)
	}
	if rec, _ := st.ReadTicket("JIG-2"); rec.Rank != nil || rec.Title != "Second" || len(rec.BlockedBy) != 1 {
		t.Errorf("record after --clear = %+v, want no rank and the rest kept", rec)
	}
	if got := subject(); got != "JIG-2: clear rank" {
		t.Errorf("store commit after --clear = %q, want \"JIG-2: clear rank\"", got)
	}

	// Calls that name no rank, or a ticket that is not there, change nothing.
	before = head()
	for _, args := range [][]string{{"JIG-2"}, {"JIG-2", "0"}, {"JIG-2", "soon"}, {"JIG-2", "3", "--clear"}, {"JIG-9", "1"}, {}} {
		out, code := runMain(t, e, "", append([]string{"rank"}, args...)...)
		if code == 0 || !strings.Contains(out, "error") {
			t.Errorf("jig rank %v: exit %d, want a refusal:\n%s", args, code, out)
		}
	}
	if head() != before {
		t.Errorf("a refused jig rank moved the store's head")
	}
}
