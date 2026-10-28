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

// writeFlat lays down the flat layout the tree had before each entry got a
// folder: the record beside the link, <remote id>.yaml, and the link named
// <remote id> (a symlink where the machine has them, else the plain file git
// leaves). text is the record exactly as the old build wrote it.
func writeFlat(t *testing.T, st *Store, e TrackerEntry, o TrackerOwner, text string) {
	t.Helper()
	flat := filepath.Join(st.Root, st.flatRecordRelPath(e))
	if err := os.MkdirAll(filepath.Dir(flat), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flat, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	link := st.TrackerPath(e)
	target := filepath.ToSlash(filepath.Join("..", "..", "..", "..", st.ownerRelDir(o)))
	if st.SymlinksOn() {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.WriteFile(link, []byte(target), 0o644); err != nil {
		t.Fatal(err)
	}
}

// flatText is the record of GitHub issue number issue of acme/tracking for
// ticket, as the flat layout kept it.
func flatText(ticket string, issue string) string {
	return treeHeader + "ticket: " + ticket + "\nrepo: acme/tracking\nissue: " + issue + "\nnode_id: I_" + issue + "\nfuture_key: kept\n"
}

// TestFlatRecordIsStillReadWhereItsFolderHasNone: a tree in the flat layout
// keeps working before any sync has moved it. Its record is found by its
// ticket, binds the ticket's branch and gives it its issue; the check reports
// the one thing to do, and a record in the entry's own folder wins over a flat
// file beside it.
func TestFlatRecordIsStillReadWhereItsFolderHasNone(t *testing.T) {
	st, _ := newTreeStore(t)
	e := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "7"}
	writeFlat(t, st, e, TrackerOwner{Ticket: "JIG-1"}, flatText("JIG-1", "7"))

	recs, err := st.TrackerRecordsFor(TrackerOwner{Ticket: "JIG-1"})
	if err != nil || len(recs) != 1 {
		t.Fatalf("TrackerRecordsFor(JIG-1) = %+v, %v; want the flat record", recs, err)
	}
	if rec := recs[0]; rec.Entry != e || !rec.Flat || rec.Rel != "trackers/github/acme/tracking/7.yaml" {
		t.Errorf("record = %+v, want entry %v read from the flat file", rec, e)
	}
	if ids, err := st.TrackerIDs("JIG-1"); err != nil || ids.GitHub == nil || ids.GitHub.Number != 7 {
		t.Errorf("TrackerIDs(JIG-1) = %+v, %v; want issue 7", ids, err)
	}
	if id, how, err := st.TicketForBranch("7-the-flat-one"); err != nil || id != "JIG-1" || how != "remote" {
		t.Errorf("TicketForBranch(7-the-flat-one) = %q, %q, %v; want JIG-1 by a remote id", id, how, err)
	}

	// The link of the flat layout sits where the folder goes, and is not a
	// second thing to report: the unmoved record is the one.
	problems, err := st.CheckTrackerTree()
	if err != nil {
		t.Fatal(err)
	}
	if got := problemCodes(problems); !reflect.DeepEqual(got, []string{TrackerRecordUnmoved}) {
		t.Fatalf("problems = %+v, want exactly the unmoved record", problems)
	}
	if p := problems[0]; p.Path != "trackers/github/acme/tracking/7.yaml" || !strings.Contains(p.Msg, "trackers/github/acme/tracking/7/record.yaml") || !strings.Contains(p.Help, "jig trackers sync") {
		t.Errorf("problem = %+v, want the flat file, the place it moves to and `jig trackers sync`", p)
	}

	// A record in the folder is the record; the flat file beside it is a
	// leftover and not a second record of the ticket.
	if err := st.WriteTrackerRecord(e, TrackerOwner{Ticket: "JIG-1"}, githubFields(t, "acme/tracking", 7), treeHeader); err != nil {
		t.Fatal(err)
	}
	writeFlatFile(t, st, e, flatText("JIG-1", "7"))
	st.ReloadTrackerTree()
	if _, err := st.RebuildTrackerLinks(); err != nil {
		t.Fatal(err)
	}
	recs, _ = st.TrackerRecordsFor(TrackerOwner{Ticket: "JIG-1"})
	if len(recs) != 1 || recs[0].Flat {
		t.Errorf("records with both = %+v, want only the one in the folder", recs)
	}
	problems, _ = st.CheckTrackerTree()
	if got := problemCodes(problems); !reflect.DeepEqual(got, []string{TrackerRecordUnmoved}) {
		t.Errorf("problems with a leftover flat file = %+v, want it reported as unmoved, and no duplicate", problems)
	}
}

// writeFlatFile writes only the flat record file of e, making its scope folder.
func writeFlatFile(t *testing.T, st *Store, e TrackerEntry, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(st.TrackerPath(e)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, st.flatRecordRelPath(e)), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestMoveFlatTrackerRecordsMovesTheRecordIntoItsFolderAndLinksIt: the record
// moves as the bytes it was, the old link at the folder's path is replaced by
// the folder with the link named by the store id inside it, a ticket's and a
// chart's alike; a second run finds nothing, and the tree is whole.
func TestMoveFlatTrackerRecordsMovesTheRecordIntoItsFolderAndLinksIt(t *testing.T) {
	st, _ := newTreeStore(t)
	skipWithoutSymlinks(t, st)
	e1 := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "7"}
	e2 := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "8"}
	text1 := flatText("JIG-1", "7")
	writeFlat(t, st, e1, TrackerOwner{Ticket: "JIG-1"}, text1)
	text2 := treeHeader + "chart: demo\nrepo: acme/tracking\nissue: 8\nnode_id: I_8\n"
	writeFlat(t, st, e2, TrackerOwner{Chart: "demo"}, text2)

	moved, err := st.MoveFlatTrackerRecords()
	if err != nil {
		t.Fatal(err)
	}
	want := []TrackerMove{
		{Owner: TrackerOwner{Ticket: "JIG-1"}, From: "trackers/github/acme/tracking/7.yaml", To: "trackers/github/acme/tracking/7/record.yaml"},
		{Owner: TrackerOwner{Chart: "demo"}, From: "trackers/github/acme/tracking/8.yaml", To: "trackers/github/acme/tracking/8/record.yaml"},
	}
	if !reflect.DeepEqual(moved, want) {
		t.Fatalf("MoveFlatTrackerRecords = %+v, want %+v", moved, want)
	}
	for e, text := range map[TrackerEntry]string{e1: text1, e2: text2} {
		if data, err := os.ReadFile(st.TrackerRecordPath(e)); err != nil || string(data) != text {
			t.Errorf("%v: moved record = %q, %v; want the bytes it was", e, data, err)
		}
		if _, err := os.Stat(filepath.Join(st.Root, st.flatRecordRelPath(e))); !os.IsNotExist(err) {
			t.Errorf("%v: the flat file is still there (stat err %v)", e, err)
		}
	}
	wantLinks := map[string]string{
		"trackers/github/acme/tracking/7/JIG-1": "../../../../../tickets/JIG-1",
		"trackers/github/acme/tracking/8/demo":  "../../../../../charts/demo",
	}
	if links := linkMap(t, st); !reflect.DeepEqual(links, wantLinks) {
		t.Errorf("links = %v, want %v", links, wantLinks)
	}
	if problems, err := st.CheckTrackerTree(); err != nil || len(problems) != 0 {
		t.Errorf("CheckTrackerTree = %+v, %v; want a whole tree", problems, err)
	}
	if ids, err := st.TrackerIDs("JIG-1"); err != nil || ids.GitHub == nil || ids.GitHub.Number != 7 {
		t.Errorf("TrackerIDs(JIG-1) after the move = %+v, %v", ids, err)
	}

	again, err := st.MoveFlatTrackerRecords()
	if err != nil || len(again) != 0 {
		t.Errorf("a second MoveFlatTrackerRecords = %+v, %v; want nothing to move", again, err)
	}
	if links, err := st.RebuildTrackerLinks(); err != nil || links.Changed() {
		t.Errorf("RebuildTrackerLinks after the move = %+v, %v; want nothing to do", links, err)
	}
}

// TestMoveFlatTrackerRecordsWithoutSymlinksMakesNoLink: a clone whose git
// keeps no symlinks has the plain file git left for the old link at the
// folder's path; the move takes it out so the folder can be made, and makes no
// link of its own.
func TestMoveFlatTrackerRecordsWithoutSymlinksMakesNoLink(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	runGit(t, work, "config", "core.symlinks", "false")
	if err := st.CreateTicketRecord("JIG-1", Ticket{Title: "One"}); err != nil {
		t.Fatal(err)
	}
	var warnings []string
	st.Warn = func(format string, args ...any) { warnings = append(warnings, format) }
	e := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "7"}
	writeFlat(t, st, e, TrackerOwner{Ticket: "JIG-1"}, flatText("JIG-1", "7"))

	moved, err := st.MoveFlatTrackerRecords()
	if err != nil || len(moved) != 1 {
		t.Fatalf("MoveFlatTrackerRecords = %+v, %v; want the one record moved", moved, err)
	}
	if fi, err := os.Stat(st.TrackerPath(e)); err != nil || !fi.IsDir() {
		t.Errorf("the entry's path = %v, %v; want a folder", fi, err)
	}
	if links := linkMap(t, st); len(links) != 0 {
		t.Errorf("links = %v, want none", links)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "makes no links") {
		t.Errorf("warnings = %q, want exactly the one that this clone makes no links", warnings)
	}
}

// TestMoveFlatTrackerRecordsDropsALeftoverAndRefusesAnotherOwners: a flat file
// beside a folder's record of the same owner is a leftover and goes; one beside
// the record of another ticket is refused, TRACKER_ENTRY_TAKEN, and both stay.
func TestMoveFlatTrackerRecordsDropsALeftoverAndRefusesAnotherOwners(t *testing.T) {
	st, _ := newTreeStore(t)
	e := putRecord(t, st, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 7)
	writeFlatFile(t, st, e, flatText("JIG-1", "7"))
	moved, err := st.MoveFlatTrackerRecords()
	if err != nil || len(moved) != 0 {
		t.Fatalf("MoveFlatTrackerRecords = %+v, %v; want nothing moved", moved, err)
	}
	if _, err := os.Stat(filepath.Join(st.Root, st.flatRecordRelPath(e))); !os.IsNotExist(err) {
		t.Errorf("the leftover flat file is still there (stat err %v)", err)
	}

	before, _ := os.ReadFile(st.TrackerRecordPath(e))
	writeFlatFile(t, st, e, flatText("JIG-2", "7"))
	_, err = st.MoveFlatTrackerRecords()
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TRACKER_ENTRY_TAKEN" || !strings.Contains(ae.Msg, "trackers/github/acme/tracking/7.yaml") {
		t.Fatalf("MoveFlatTrackerRecords onto another owner's record = %v, want TRACKER_ENTRY_TAKEN naming the flat file", err)
	}
	if _, err := os.Stat(filepath.Join(st.Root, st.flatRecordRelPath(e))); err != nil {
		t.Errorf("the refused flat file is gone: %v", err)
	}
	if after, _ := os.ReadFile(st.TrackerRecordPath(e)); string(after) != string(before) {
		t.Errorf("the record changed:\n%s", after)
	}
}

// TestMoveFlatTrackerRecordsLeavesAnUnreadableRecordForTheCheck: a flat record
// that cannot be decoded stays where it is, and the check reports it.
func TestMoveFlatTrackerRecordsLeavesAnUnreadableRecordForTheCheck(t *testing.T) {
	st, _ := newTreeStore(t)
	e := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "9"}
	writeFlatFile(t, st, e, "ticket: [unclosed\n")
	moved, err := st.MoveFlatTrackerRecords()
	if err != nil || len(moved) != 0 {
		t.Fatalf("MoveFlatTrackerRecords = %+v, %v; want nothing moved", moved, err)
	}
	problems, err := st.CheckTrackerTree()
	if err != nil {
		t.Fatal(err)
	}
	got := problemCodes(problems)
	want := []string{TrackerRecordInvalid, TrackerRecordUnmoved}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("problem codes = %v, want %v", got, want)
	}
}

// TestWriteTrackerRecordOverTheFlatLayoutNeverWritesThroughTheOldLink: the link
// of the flat layout sits where the entry's folder goes and leads to the
// ticket's own folder, so a write that made the folder by following it would
// put record.yaml into the ticket. It takes the link out first, and the flat
// record goes with it.
func TestWriteTrackerRecordOverTheFlatLayoutNeverWritesThroughTheOldLink(t *testing.T) {
	st, _ := newTreeStore(t)
	skipWithoutSymlinks(t, st)
	e := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "7"}
	writeFlat(t, st, e, TrackerOwner{Ticket: "JIG-1"}, flatText("JIG-1", "7"))

	if err := st.WriteTrackerRecord(e, TrackerOwner{Ticket: "JIG-1"}, githubFields(t, "acme/tracking", 7), treeHeader); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(st.TicketDir("JIG-1"), "record.yaml")); !os.IsNotExist(err) {
		t.Errorf("the record was written into the ticket's own folder (stat err %v)", err)
	}
	if fi, err := os.Lstat(st.TrackerPath(e)); err != nil || !fi.IsDir() {
		t.Fatalf("the entry's path = %v, %v; want a folder", fi, err)
	}
	if _, err := os.Stat(st.TrackerRecordPath(e)); err != nil {
		t.Errorf("the record is not in the entry's folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(st.Root, st.flatRecordRelPath(e))); !os.IsNotExist(err) {
		t.Errorf("the flat record is still there (stat err %v)", err)
	}
	if _, err := st.RebuildTrackerLinks(); err != nil {
		t.Fatal(err)
	}
	if problems, err := st.CheckTrackerTree(); err != nil || len(problems) != 0 {
		t.Errorf("CheckTrackerTree = %+v, %v; want a whole tree", problems, err)
	}
}

// TestRemoveTrackerRecordTakesTheFlatLayoutToo: the flat record and the link at
// the folder's path go, and the scope folders they leave empty.
func TestRemoveTrackerRecordTakesTheFlatLayoutToo(t *testing.T) {
	st, _ := newTreeStore(t)
	e := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "7"}
	writeFlat(t, st, e, TrackerOwner{Ticket: "JIG-1"}, flatText("JIG-1", "7"))
	if err := st.RemoveTrackerRecord(e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(st.Root, "trackers", "github")); !os.IsNotExist(err) {
		t.Errorf("trackers/github is still there (lstat err %v)", err)
	}
	if recs, _ := st.TrackerRecords(); len(recs) != 0 {
		t.Errorf("records = %+v, want none", recs)
	}
}

// TestAFlatEntryNeverReadsOrRemovesAFileOfTheFolderItLinksTo: in the flat layout
// the entry's path is a link to the ticket's or chart's own folder, so
// <entry>/record.yaml reached through it is a file of the chart's. Moving the
// entry, and removing it, treat the path as no folder of the tree's until it is
// one, and leave that file as it is.
func TestAFlatEntryNeverReadsOrRemovesAFileOfTheFolderItLinksTo(t *testing.T) {
	st, _ := newTreeStore(t)
	skipWithoutSymlinks(t, st)
	own := filepath.Join(st.Root, "charts", "demo", "record.yaml")
	const ownText = "chart: demo\nsomething: the chart's own file\n"
	if err := os.WriteFile(own, []byte(ownText), 0o644); err != nil {
		t.Fatal(err)
	}
	moves := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "7"}
	removes := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "8"}
	text := treeHeader + "chart: demo\nrepo: acme/tracking\nissue: 7\nnode_id: I_7\n"
	writeFlat(t, st, moves, TrackerOwner{Chart: "demo"}, text)
	writeFlat(t, st, removes, TrackerOwner{Chart: "demo"}, treeHeader+"chart: demo\nrepo: acme/tracking\nissue: 8\nnode_id: I_8\n")

	if err := st.RemoveTrackerRecord(removes); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(own); err != nil || string(got) != ownText {
		t.Fatalf("the chart's own record.yaml after removing a flat entry = %q, %v; want it untouched", got, err)
	}
	if _, err := os.Lstat(st.TrackerPath(removes)); !os.IsNotExist(err) {
		t.Errorf("the removed entry's old link is still there (lstat err %v)", err)
	}

	moved, err := st.MoveFlatTrackerRecords()
	if err != nil || len(moved) != 1 {
		t.Fatalf("MoveFlatTrackerRecords = %+v, %v; want the one record moved, not refused over the chart's file", moved, err)
	}
	if got, err := os.ReadFile(own); err != nil || string(got) != ownText {
		t.Errorf("the chart's own record.yaml after the move = %q, %v; want it untouched", got, err)
	}
	if got, err := os.ReadFile(st.TrackerRecordPath(moves)); err != nil || string(got) != text {
		t.Errorf("the moved record = %q, %v; want the flat record's bytes", got, err)
	}
}

// TestLinkNameOKRefusesAnythingThatIsTheRecordOrLeavesTheFolder: the record's
// name is matched the way Windows and macOS match it, without regard to case
// and trailing dots and spaces, so a chart cannot name a link over the record.
func TestLinkNameOKRefusesAnythingThatIsTheRecordOrLeavesTheFolder(t *testing.T) {
	for name, want := range map[string]bool{
		"JIG-1": true, "demo": true, "record.yaml.bak": true, "my.record.yaml": true,
		"record.yaml": false, "RECORD.YAML": false, "Record.Yaml": false, "record.yaml.": false,
		"record.yaml ": false, "": false, ".": false, "..": false, "a/b": false, `a\b`: false,
	} {
		if got := (TrackerOwner{Chart: name}).linkNameOK(); got != want {
			t.Errorf("linkNameOK(%q) = %v, want %v", name, got, want)
		}
	}
}
