package mirror

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/store"
)

// TestClosedTicketStatus: a closure wins over everything else a ticket is,
// covered is Done and the others are Not planned, and an open issue is wanted
// only for a ticket that is not closed.
func TestClosedTicketStatus(t *testing.T) {
	st := &store.Store{Root: t.TempDir()}
	merged := []PRRef{{Owner: "o", Repo: "r", Number: 1, State: "MERGED"}}
	open := []PRRef{{Owner: "o", Repo: "r", Number: 2, State: "OPEN"}}
	for _, tc := range []struct {
		ticket string
		closed *store.Closed
		prs    []PRRef
		want   string
	}{
		{"DEMO-1", &store.Closed{Reason: store.CloseCovered, By: "o/r#82"}, nil, "Done"},
		{"DEMO-2", &store.Closed{Reason: store.CloseCovered, By: "o/r#82"}, open, "Done"},
		{"DEMO-3", &store.Closed{Reason: store.CloseSuperseded, By: "DEMO-1"}, merged, "Not planned"},
		{"DEMO-4", &store.Closed{Reason: store.CloseNotPlanned}, open, "Not planned"},
		{"DEMO-5", nil, open, "In review"},
	} {
		if err := st.CreateTicketRecord(tc.ticket, store.Ticket{Closed: tc.closed}); err != nil {
			t.Fatal(err)
		}
		got, err := TicketStatus(st, tc.ticket, tc.prs)
		if err != nil || got != tc.want {
			t.Errorf("%s: Status = %q (err %v), want %q", tc.ticket, got, err, tc.want)
		}
		if IsOpen(got) != (tc.closed == nil) {
			t.Errorf("%s: IsOpen(%q) = %v for a ticket closed = %v", tc.ticket, got, IsOpen(got), tc.closed != nil)
		}
	}
}

// rewriteTestRecord replaces the tracker record of what with rec, as a record
// written by an earlier sync (or the bridge) would read.
func rewriteTestRecord(t *testing.T, st *store.Store, what string, rec githubRecord) {
	t.Helper()
	var fields yaml.Node
	if err := fields.Encode(rec); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteTrackerRecord(githubEntry(rec.Repo, rec.Issue), ownerOfWhat(what), &fields, recordHeader); err != nil {
		t.Fatal(err)
	}
}
