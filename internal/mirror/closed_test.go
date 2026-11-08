package mirror

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/store"
)

// closeForTest closes ticket the way `jig close` records it, and commits the
// record like the checkpoint after it does.
func closeForTest(t *testing.T, st *store.Store, ticket, reason, by, note string) {
	t.Helper()
	if err := st.CloseTicket(ticket, store.NewClosed(reason, by, note, time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
	runGit(t, st.Root, "add", "-A")
	runGit(t, st.Root, "commit", "-m", ticket+": closed")
}

// TestClosedTicketIssueClosesOnSync is the critical path of closing a ticket
// without a pull request, through the fake client: the next sync closes the
// issue of a covered ticket as completed with Done, closes the issue of a
// superseded or not-planned ticket as not planned with the Not planned Status,
// writes how it was closed into the body, and a later sync leaves all of it
// alone. A ticket reopened gets its issue back open with the Status its state
// derives.
func TestClosedTicketIssueClosesOnSync(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "Covered by other work")
	mintTestTicket(t, st, "DEMO-2", "Replaced by another")
	mintTestTicket(t, st, "DEMO-3", "Decided against")
	mintTestTicket(t, st, "DEMO-4", "Still to build")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{}
	if _, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	node := func(ticket string) string { return readTestRecord(t, treeRecordPath(t, st, ticket)).NodeID }
	for _, id := range []string{"DEMO-1", "DEMO-2", "DEMO-3", "DEMO-4"} {
		if !client.issue(node(id)).Open {
			t.Fatalf("%s has a closed issue before anything was closed", id)
		}
	}

	closeForTest(t, st, "DEMO-1", store.CloseCovered, "example/product#82", "The demo kit shipped it.\nNothing is left.")
	closeForTest(t, st, "DEMO-2", store.CloseSuperseded, "DEMO-4", "")
	closeForTest(t, st, "DEMO-3", store.CloseNotPlanned, "", "We record demos from the e2e runs instead.")

	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if want := []string{node("DEMO-1")}; !reflect.DeepEqual(client.closedDone, want) {
		t.Errorf("closed as completed = %v, want only the issue of the covered ticket %v", client.closedDone, want)
	}
	if want := []string{node("DEMO-2"), node("DEMO-3")}; !reflect.DeepEqual(client.closed, want) {
		t.Errorf("closed as not planned = %v, want the issues of the superseded and the not-planned ticket %v", client.closed, want)
	}
	for id, want := range map[string]string{"DEMO-1": "completed", "DEMO-2": "not planned", "DEMO-3": "not planned"} {
		if is := client.issue(node(id)); is.Open || is.Reason != want {
			t.Errorf("%s has an issue open %v, closed as %q, want it closed as %q", id, is.Open, is.Reason, want)
		}
	}
	if !client.issue(node("DEMO-4")).Open {
		t.Errorf("DEMO-4 was not closed, and its issue is closed")
	}
	if len(report.Drift) != 0 {
		t.Errorf("drift = %+v, want none: the store says closed, so the mirror agrees", report.Drift)
	}

	// The board: the covered ticket is Done, the other two have a Status of
	// their own.
	placed := map[string]string{}
	for _, p := range client.placedItems {
		placed[p.StoreID] = p.Status
	}
	for id, want := range map[string]string{"DEMO-1": "Done", "DEMO-2": "Not planned", "DEMO-3": "Not planned", "DEMO-4": "Backlog"} {
		if placed[id] != want {
			t.Errorf("%s was placed under %q, want %q (all placed: %v)", id, placed[id], want, placed)
		}
	}

	// The body says how each was closed, with the note after it, and names the
	// issue of the superseding ticket.
	body := func(id string) string { return client.issue(node(id)).Body }
	for id, want := range map[string]string{
		"DEMO-1": "**Closed:** covered by example/product#82\n\nThe demo kit shipped it. Nothing is left.",
		"DEMO-2": "**Closed:** superseded by DEMO-4 (#4)",
		"DEMO-3": "**Closed:** not planned\n\nWe record demos from the e2e runs instead.",
	} {
		if !strings.Contains(body(id), want) {
			t.Errorf("%s has a body that lacks %q:\n%s", id, want, body(id))
		}
		if !strings.HasSuffix(body(id), ticketFooter(id)) {
			t.Errorf("%s has a body that does not end with its footer:\n%s", id, body(id))
		}
	}
	if strings.Contains(body("DEMO-4"), "Closed:") {
		t.Errorf("DEMO-4 is open and its body says it is closed:\n%s", body("DEMO-4"))
	}

	// A later sync keeps it all: no close, no reopen, no write of any kind.
	closedBefore, notPlannedBefore, updatesBefore, placedBefore := len(client.closedDone), len(client.closed), len(client.updates), len(client.placedItems)
	report, err = Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("third Sync: %v", err)
	}
	if len(client.closedDone) != closedBefore || len(client.closed) != notPlannedBefore || len(client.reopened) != 0 ||
		len(client.updates) != updatesBefore || len(client.placedItems) != placedBefore {
		t.Errorf("a sync of closed tickets whose issues are closed wrote again: closed %v, not planned %v, reopened %v, updates %d, placed %d",
			client.closedDone, client.closed, client.reopened, len(client.updates)-updatesBefore, len(client.placedItems)-placedBefore)
	}
	if len(report.Drift) != 0 || len(report.Updated) != 0 || len(report.Placed) != 0 {
		t.Errorf("third sync reported drift %+v, updates %+v, placements %+v, want none", report.Drift, report.Updated, report.Placed)
	}

	// Reopened: the issue opens again with the Status the state of the ticket
	// derives, and the body no longer says it is closed.
	if err := st.ReopenTicket("DEMO-3"); err != nil {
		t.Fatal(err)
	}
	runGit(t, st.Root, "add", "-A")
	runGit(t, st.Root, "commit", "-m", "DEMO-3: reopened")
	if _, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{}); err != nil {
		t.Fatalf("fourth Sync: %v", err)
	}
	if want := []string{node("DEMO-3")}; !reflect.DeepEqual(client.reopened, want) {
		t.Errorf("reopened = %v, want %v", client.reopened, want)
	}
	if is := client.issue(node("DEMO-3")); !is.Open || strings.Contains(is.Body, "Closed:") {
		t.Errorf("DEMO-3 has an issue after jig reopen open %v, with body\n%s", is.Open, is.Body)
	}
	last := client.placedItems[len(client.placedItems)-1]
	if last.StoreID != "DEMO-3" || last.Status != "Backlog" {
		t.Errorf("the reopened ticket was placed as %+v, want Backlog", last)
	}
}

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

// TestSyncReportsNoDriftWhenGitHubAlreadyHoldsTheStoresValue: GitHub's own
// automation closed the issue and set its item to Done before the sync that
// carries the store's closure. GitHub then holds exactly what the sync would
// write, so nothing is overwritten: no drift, no write, and synced: records
// the value.
func TestSyncReportsNoDriftWhenGitHubAlreadyHoldsTheStoresValue(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "Covered by other work")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{}
	if _, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	rec := readTestRecord(t, treeRecordPath(t, st, "DEMO-1"))

	// GitHub closes the issue and moves the item to Done on its own.
	is := client.issue(rec.NodeID)
	is.Open = false
	is.Reason = "completed"
	client.itemFields[rec.Item] = fakeItemFields{Status: "Done", StoreID: "DEMO-1"}

	closeForTest(t, st, "DEMO-1", store.CloseCovered, "example/product#82", "Shipped.")

	closedBefore, notPlannedBefore, reopenedBefore, placedBefore := len(client.closedDone), len(client.closed), len(client.reopened), len(client.placedItems)

	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if len(report.Drift) != 0 {
		t.Errorf("drift = %+v, want none: GitHub already holds the store's value", report.Drift)
	}
	if len(client.closedDone) != closedBefore || len(client.closed) != notPlannedBefore || len(client.reopened) != reopenedBefore || len(client.placedItems) != placedBefore {
		t.Errorf("a write was sent for a value GitHub already holds")
	}
	got := readTestRecord(t, treeRecordPath(t, st, "DEMO-1"))
	if got.Synced == nil || got.Synced.State != "CLOSED" || got.Synced.Status != "Done" || got.Synced.StoreID != "DEMO-1" {
		t.Errorf("synced = %+v, want CLOSED / Done / DEMO-1", got.Synced)
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

// TestSyncReportsNoStoreIDDriftWhenGitHubHoldsTheStoresValueButNotSynced: the
// Store ID on GitHub already equals the store's value, though it differs from
// what synced: last recorded (the record is stale, say after a first sync
// elsewhere). GitHub holds what the sync would write, so nothing is
// overwritten and nothing drifted.
func TestSyncReportsNoStoreIDDriftWhenGitHubHoldsTheStoresValueButNotSynced(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "Covered by other work")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{}
	if _, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	rec := readTestRecord(t, treeRecordPath(t, st, "DEMO-1"))
	if got := client.itemFields[rec.Item]; got.StoreID != "DEMO-1" {
		t.Fatalf("board Store ID after the first sync = %q, want DEMO-1", got.StoreID)
	}
	rec.Synced.StoreID = "DEMO-OLD"
	rewriteTestRecord(t, st, "DEMO-1", rec)

	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	for _, d := range report.Drift {
		t.Errorf("drift = %+v, want none: GitHub's Store ID already equals the store's value", d)
	}
}

// TestSyncReportsNoDriftOnAFirstSyncOfARecordWithNoSyncedBoardFields: a record
// has its item but its synced: holds no Status and no Store ID (nothing the
// sync wrote to compare), and the board differs. This is the first sync of
// those fields, not drift: the board is set and synced: records it.
func TestSyncReportsNoDriftOnAFirstSyncOfARecordWithNoSyncedBoardFields(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{}
	if _, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	rec := readTestRecord(t, treeRecordPath(t, st, "DEMO-1"))
	rec.Synced.Status, rec.Synced.StoreID = "", ""
	rewriteTestRecord(t, st, "DEMO-1", rec)
	client.itemFields[rec.Item] = fakeItemFields{Status: "In progress", StoreID: "SOMEONE-ELSES"}

	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	for _, d := range report.Drift {
		t.Errorf("drift = %+v, want none: synced: holds no Status or Store ID to compare", d)
	}
	got := readTestRecord(t, treeRecordPath(t, st, "DEMO-1"))
	if got.Synced == nil || got.Synced.Status != "Backlog" || got.Synced.StoreID != "DEMO-1" {
		t.Errorf("synced = %+v, want Backlog / DEMO-1 recorded", got.Synced)
	}
}
