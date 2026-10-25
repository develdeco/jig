package store

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// writeLegacy writes the record a build from before the tracker tree kept in
// the folder of a ticket (or of a chart, when chart is set).
func writeLegacy(t *testing.T, st *Store, o TrackerOwner, text string) string {
	t.Helper()
	abs := filepath.Join(st.Root, st.legacyRecordRel(o))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return abs
}

// TestTrackerIDsReadTheTreeThenTheOldPlace: a ticket is found by the record
// of the tree that names it; a ticket with none in the tree is found by the
// record in its own folder, so a store not migrated yet still binds; with
// both, the tree wins.
func TestTrackerIDsReadTheTreeThenTheOldPlace(t *testing.T) {
	st, _ := newTreeStore(t)
	putRecord(t, st, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 102)
	writeLegacy(t, st, TrackerOwner{Ticket: "JIG-2"}, "repo: acme/tracking\nissue: 7\nnode_id: I_7\n")
	for ticket, want := range map[string]int{"JIG-1": 102, "JIG-2": 7} {
		got, err := st.TrackerIDs(ticket)
		if err != nil || got.GitHub == nil || got.GitHub.Number != want || got.GitHub.Repo != "acme/tracking" {
			t.Errorf("TrackerIDs(%s) = %+v, %v; want acme/tracking#%d", ticket, got, err, want)
		}
	}

	writeLegacy(t, st, TrackerOwner{Ticket: "JIG-1"}, "repo: acme/tracking\nissue: 3\nnode_id: I_3\n")
	if got, _ := st.TrackerIDs("JIG-1"); got.GitHub == nil || got.GitHub.Number != 102 {
		t.Errorf("TrackerIDs(JIG-1) with both = %+v, want the tree record, 102", got)
	}

	// A record in the old place that cannot be decoded is still the mirror
	// to mend, and says so.
	st.ReloadTrackerTree()
	writeLegacy(t, st, TrackerOwner{Ticket: "JIG-2"}, "repo: [unclosed\n")
	_, err := st.TrackerIDs("JIG-2")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TRACKER_RECORD_INVALID" {
		t.Errorf("TrackerIDs of a broken old record: %v, want TRACKER_RECORD_INVALID", err)
	}
}

// TestBranchBindsByRemoteIDViaTheTree: a branch led by an issue number binds
// to the ticket the tree gives that issue, and to the one whose record is
// still in its own folder; an issue number no record holds, or a record of a
// ticket the store lacks, binds nothing; the same number in two repositories
// for two tickets is refused.
func TestBranchBindsByRemoteIDViaTheTree(t *testing.T) {
	st, _ := newTreeStore(t)
	putRecord(t, st, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 102)
	writeLegacy(t, st, TrackerOwner{Ticket: "JIG-2"}, "repo: acme/tracking\nissue: 7\nnode_id: I_7\n")
	putRecord(t, st, TrackerOwner{Ticket: "NOPE-1"}, "acme/tracking", 55)

	for branch, want := range map[string]string{
		"102-upload-item-gallery": "JIG-1",
		"102":                     "JIG-1",
		"7-from-the-old-place":    "JIG-2",
		"55-no-such-ticket":       "",
		"103-no-such-record":      "",
	} {
		id, how, err := st.TicketForBranch(branch)
		if err != nil || id != want || (want != "" && how != "remote") {
			t.Errorf("TicketForBranch(%q) = %q, %q, %v; want %q by a remote id", branch, id, how, err, want)
		}
	}

	// The tree is read afresh: a record that lands now binds at once.
	putRecord(t, st, TrackerOwner{Ticket: "JIG-2"}, "acme/tracking", 200)
	if id, _, err := st.TicketForBranch("200-fresh"); err != nil || id != "JIG-2" {
		t.Errorf("TicketForBranch(200-fresh) = %q, %v; want JIG-2", id, err)
	}

	putRecord(t, st, TrackerOwner{Ticket: "JIG-2"}, "acme/elsewhere", 102)
	_, _, err := st.TicketForBranch("102-which")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TICKET_REMOTE_ID_COLLISION" {
		t.Errorf("TicketForBranch(102-which) = %v, want TICKET_REMOTE_ID_COLLISION", err)
	}
}

// TestLegacyTrackerRecordsListTheOldPlaces: the records still in a ticket or
// chart folder, tickets first.
func TestLegacyTrackerRecordsListTheOldPlaces(t *testing.T) {
	st, _ := newTreeStore(t)
	writeLegacy(t, st, TrackerOwner{Chart: "demo"}, "repo: acme/tracking\nissue: 3\n")
	writeLegacy(t, st, TrackerOwner{Ticket: "JIG-2"}, "repo: acme/tracking\nissue: 2\n")
	got, err := st.LegacyTrackerRecords()
	if err != nil {
		t.Fatal(err)
	}
	var rels []string
	for _, l := range got {
		rels = append(rels, filepath.ToSlash(l.Rel))
	}
	want := []string{"tickets/JIG-2/tracker/github.yaml", "charts/demo/github.yaml"}
	if !reflect.DeepEqual(rels, want) {
		t.Errorf("LegacyTrackerRecords = %v, want %v", rels, want)
	}
}

// TestMoveLegacyTrackerRecord: the tree record names the owner and keeps every
// key of the old one, including one the tree has never heard of; the old file
// and the folder it leaves empty go; a tree record of the same owner already
// there makes the old file a leftover to drop; one of another owner is refused
// and nothing moves.
func TestMoveLegacyTrackerRecord(t *testing.T) {
	st, _ := newTreeStore(t)
	old := writeLegacy(t, st, TrackerOwner{Ticket: "JIG-1"}, treeHeader+"repo: acme/tracking\nissue: 5\nnode_id: I_5\nfuture_key: kept\n")
	l, ok, err := st.LegacyTrackerRecord(TrackerOwner{Ticket: "JIG-1"})
	if err != nil || !ok {
		t.Fatalf("LegacyTrackerRecord = %+v, %v, %v", l, ok, err)
	}
	e := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "5"}
	if err := st.MoveLegacyTrackerRecord(l, e, treeHeader); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(st.TrackerRecordPath(e))
	if err != nil {
		t.Fatal(err)
	}
	want := treeHeader + "ticket: JIG-1\nrepo: acme/tracking\nissue: 5\nnode_id: I_5\nfuture_key: kept\n"
	if string(data) != want {
		t.Errorf("moved record =\n%s\nwant\n%s", data, want)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("the old record is still there (stat err %v)", err)
	}
	if _, err := os.Stat(filepath.Dir(old)); !os.IsNotExist(err) {
		t.Errorf("the empty tracker/ folder is still there (stat err %v)", err)
	}
	if ids, err := st.TrackerIDs("JIG-1"); err != nil || ids.GitHub == nil || ids.GitHub.Number != 5 {
		t.Errorf("TrackerIDs after the move = %+v, %v", ids, err)
	}

	// The same owner at the same entry: a leftover.
	left := writeLegacy(t, st, TrackerOwner{Ticket: "JIG-1"}, "repo: acme/tracking\nissue: 5\n")
	l, _, _ = st.LegacyTrackerRecord(TrackerOwner{Ticket: "JIG-1"})
	if err := st.MoveLegacyTrackerRecord(l, e, treeHeader); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(left); !os.IsNotExist(err) {
		t.Errorf("the leftover old record is still there (stat err %v)", err)
	}

	// Another owner at that entry: refused, and both stay.
	other := writeLegacy(t, st, TrackerOwner{Ticket: "JIG-2"}, "repo: acme/tracking\nissue: 5\n")
	l, _, _ = st.LegacyTrackerRecord(TrackerOwner{Ticket: "JIG-2"})
	err = st.MoveLegacyTrackerRecord(l, e, treeHeader)
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TRACKER_ENTRY_TAKEN" || !strings.Contains(ae.Msg, "tickets/JIG-2/tracker/github.yaml") {
		t.Fatalf("MoveLegacyTrackerRecord onto another owner = %v, want TRACKER_ENTRY_TAKEN naming the old file", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("the refused old record is gone: %v", err)
	}
	if got, _ := os.ReadFile(st.TrackerRecordPath(e)); string(got) != want {
		t.Errorf("the entry changed: %s", got)
	}
}
