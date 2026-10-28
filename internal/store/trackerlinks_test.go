package store

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// TestRebuildTrackerLinksMakesEveryLinkFromTheRecords: each record that names
// a ticket or chart gets a relative symlink to its folder in its entry's
// folder, named by the store id, with no provider anywhere; a link that is gone
// or points elsewhere is made again, a link with no record is taken out, and
// the tree comes out the same every time.
func TestRebuildTrackerLinksMakesEveryLinkFromTheRecords(t *testing.T) {
	st, warnings := newTreeStore(t)
	skipWithoutSymlinks(t, st)
	putRecord(t, st, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 11)
	putRecord(t, st, TrackerOwner{Ticket: "JIG-2"}, "Acme/Tracking", 12)
	putRecord(t, st, TrackerOwner{Chart: "demo"}, "acme/tracking", 13)

	got, err := st.RebuildTrackerLinks()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Made) != 3 || got.Off || len(*warnings) != 0 {
		t.Fatalf("RebuildTrackerLinks = %+v, warnings %v; want three links made, no warning", got, *warnings)
	}
	want := map[string]string{
		"trackers/github/acme/tracking/11/JIG-1": "../../../../../tickets/JIG-1",
		"trackers/github/acme/tracking/12/JIG-2": "../../../../../tickets/JIG-2",
		"trackers/github/acme/tracking/13/demo":  "../../../../../charts/demo",
	}
	if links := linkMap(t, st); !reflect.DeepEqual(links, want) {
		t.Fatalf("links = %v, want %v", links, want)
	}
	// A link is the ticket folder itself, read through it.
	if _, err := os.Stat(filepath.Join(st.Root, "trackers", "github", "acme", "tracking", "11", "JIG-1", "ticket.yaml")); err != nil {
		t.Errorf("the link does not lead to the ticket folder: %v", err)
	}

	again, err := st.RebuildTrackerLinks()
	if err != nil || again.Changed() {
		t.Fatalf("a second RebuildTrackerLinks = %+v, %v; want nothing to do", again, err)
	}

	// Break it three ways: a link gone, a link pointing at the wrong ticket,
	// and a link in a folder no record is in.
	root := filepath.Join(st.Root, "trackers", "github", "acme", "tracking")
	if err := os.Remove(filepath.Join(root, "11", "JIG-1")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "12", "JIG-2")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../../../../tickets/JIG-1", filepath.Join(root, "12", "JIG-2")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "99"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../../../../tickets/JIG-2", filepath.Join(root, "99", "JIG-2")); err != nil {
		t.Fatal(err)
	}
	fixed, err := st.RebuildTrackerLinks()
	if err != nil {
		t.Fatal(err)
	}
	if len(fixed.Made) != 1 || len(fixed.Replaced) != 1 || len(fixed.Removed) != 1 {
		t.Errorf("RebuildTrackerLinks = %+v, want one made, one replaced and one removed", fixed)
	}
	if links := linkMap(t, st); !reflect.DeepEqual(links, want) {
		t.Errorf("links after the repair = %v, want the same tree %v", links, want)
	}
	if _, err := os.Lstat(filepath.Join(root, "99")); !os.IsNotExist(err) {
		t.Errorf("the folder that held only a link with no record is still there (lstat err %v)", err)
	}
}

// TestRebuildTrackerLinksLeavesARecordNamingNothingUnlinked: a record whose
// ticket the store does not have gets no link (it would lead nowhere), and
// neither does a record that names both a ticket and a chart.
func TestRebuildTrackerLinksLeavesARecordNamingNothingUnlinked(t *testing.T) {
	st, _ := newTreeStore(t)
	skipWithoutSymlinks(t, st)
	putRecord(t, st, TrackerOwner{Ticket: "JIG-9"}, "acme/tracking", 21)
	both := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "22"}
	if err := os.MkdirAll(filepath.Dir(st.TrackerRecordPath(both)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.TrackerRecordPath(both), []byte("ticket: JIG-1\nchart: demo\nrepo: acme/tracking\nissue: 22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := st.RebuildTrackerLinks()
	if err != nil || got.Changed() {
		t.Fatalf("RebuildTrackerLinks = %+v, %v; want no link made", got, err)
	}
	if links := linkMap(t, st); len(links) != 0 {
		t.Errorf("links = %v, want none", links)
	}
}

// TestRebuildTrackerLinksWithoutSymlinksKeepsEveryRecord: a clone whose git
// keeps no symlinks gets no links, and the one warning, once however often it
// is asked; every record is where it was, and a plain file git left where a
// link would be stays as it is.
func TestRebuildTrackerLinksWithoutSymlinksKeepsEveryRecord(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	runGit(t, work, "config", "core.symlinks", "false")
	if err := st.CreateTicketRecord("JIG-1", Ticket{Title: "One"}); err != nil {
		t.Fatal(err)
	}
	var warnings []string
	st.Warn = func(format string, args ...any) { warnings = append(warnings, format) }
	e := putRecord(t, st, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 31)
	standIn := st.TrackerLinkPath(e, TrackerOwner{Ticket: "JIG-1"})
	if err := os.WriteFile(standIn, []byte("../../../../../tickets/JIG-1"), 0o644); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		got, err := st.RebuildTrackerLinks()
		if err != nil || !got.Off || got.Changed() {
			t.Fatalf("RebuildTrackerLinks = %+v, %v; want Off and nothing touched", got, err)
		}
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "makes no links") {
		t.Errorf("warnings = %q, want exactly one saying this clone makes no links", warnings)
	}
	if data, err := os.ReadFile(standIn); err != nil || string(data) != "../../../../../tickets/JIG-1" {
		t.Errorf("the stand-in file = %q, %v; want it left as it is", data, err)
	}
	if recs, _ := st.TrackerRecordsFor(TrackerOwner{Ticket: "JIG-1"}); len(recs) != 1 {
		t.Errorf("records = %+v, want the one record kept", recs)
	}
	if problems, err := st.CheckTrackerTree(); err != nil || len(problems) != 0 {
		t.Errorf("CheckTrackerTree = %+v, %v; want nothing wrong without links to check", problems, err)
	}
}

// folderNames lists the names in the entry folder of e.
func folderNames(t *testing.T, st *Store, e TrackerEntry) []string {
	t.Helper()
	ents, err := os.ReadDir(st.TrackerPath(e))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, ent := range ents {
		names = append(names, ent.Name())
	}
	sort.Strings(names)
	return names
}

// TestAnEntryIsAFolderOfTheRecordAndTheLinkNamedByTheStoreID: each remote item
// is one folder holding record.yaml and the link, named by the ticket's id or
// the chart's name, so the store id reads in the tree without opening the link.
func TestAnEntryIsAFolderOfTheRecordAndTheLinkNamedByTheStoreID(t *testing.T) {
	st, _ := newTreeStore(t)
	skipWithoutSymlinks(t, st)
	ticket := putRecord(t, st, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 101)
	chart := putRecord(t, st, TrackerOwner{Chart: "demo"}, "acme/tracking", 102)
	if _, err := st.RebuildTrackerLinks(); err != nil {
		t.Fatal(err)
	}
	if got, want := folderNames(t, st, ticket), []string{"JIG-1", "record.yaml"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the ticket's entry holds %v, want %v", got, want)
	}
	if got, want := folderNames(t, st, chart), []string{"demo", "record.yaml"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the chart's entry holds %v, want %v", got, want)
	}
	if got, want := filepath.ToSlash(st.TrackerRelPath(ticket)), "trackers/github/acme/tracking/101"; got != want {
		t.Errorf("TrackerRelPath = %s, want %s", got, want)
	}
	// Through the link, the ticket's own record is the ticket's, and the
	// entry's record is a different file.
	if _, err := os.Stat(filepath.Join(st.TrackerPath(ticket), "JIG-1", "ticket.yaml")); err != nil {
		t.Errorf("101/JIG-1/ticket.yaml, the ticket's own record: %v", err)
	}
}

// TestRebuildTrackerLinksRenamesTheLinkWhenTheTicketIsRenamed: a ticket whose
// id changed keeps its record, which still names the old id and resolves
// through the alias; the link is named by the id the ticket has now, and the
// one under the old id is taken out.
func TestRebuildTrackerLinksRenamesTheLinkWhenTheTicketIsRenamed(t *testing.T) {
	st, _ := newTreeStore(t)
	skipWithoutSymlinks(t, st)
	if err := st.CreateTicketRecord("OLD-7", Ticket{Title: "Before the rename"}); err != nil {
		t.Fatal(err)
	}
	e := putRecord(t, st, TrackerOwner{Ticket: "OLD-7"}, "acme/tracking", 5)
	if _, err := st.RebuildTrackerLinks(); err != nil {
		t.Fatal(err)
	}
	if got, want := folderNames(t, st, e), []string{"OLD-7", "record.yaml"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the entry holds %v, want %v", got, want)
	}

	if err := os.RemoveAll(st.TicketDir("OLD-7")); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTicketRecord("NEW-1", Ticket{Title: "After the rename", Aliases: []string{"OLD-7"}}); err != nil {
		t.Fatal(err)
	}
	st.ReloadTrackerTree()
	problems, err := st.CheckTrackerTree()
	if err != nil {
		t.Fatal(err)
	}
	if got := problemCodes(problems); !reflect.DeepEqual(got, []string{TrackerLinkWrong}) || problems[0].Path != "trackers/github/acme/tracking/5/NEW-1" {
		t.Errorf("problems before the rebuild = %+v, want the link under the new id missing", problems)
	}

	got, err := st.RebuildTrackerLinks()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Made, []string{"trackers/github/acme/tracking/5/NEW-1"}) || !reflect.DeepEqual(got.Removed, []string{"trackers/github/acme/tracking/5/OLD-7"}) {
		t.Errorf("RebuildTrackerLinks = %+v, want NEW-1 made and OLD-7 removed", got)
	}
	want := map[string]string{"trackers/github/acme/tracking/5/NEW-1": "../../../../../tickets/NEW-1"}
	if links := linkMap(t, st); !reflect.DeepEqual(links, want) {
		t.Errorf("links = %v, want %v", links, want)
	}
	if problems, err := st.CheckTrackerTree(); err != nil || len(problems) != 0 {
		t.Errorf("CheckTrackerTree after the rebuild = %+v, %v; want a whole tree", problems, err)
	}
	if again, err := st.RebuildTrackerLinks(); err != nil || again.Changed() {
		t.Errorf("a second RebuildTrackerLinks = %+v, %v; want nothing to do", again, err)
	}
}

// TestALinkIsNeverNamedOutsideItsFolderOrOverItsRecord: the link is named by
// the id a record's ticket: or chart: key holds, a key read out of a file, so an
// id that is a path or the record's own name makes no link, is refused if asked
// for, and is reported as a record that names no ticket or chart.
func TestALinkIsNeverNamedOutsideItsFolderOrOverItsRecord(t *testing.T) {
	st, _ := newTreeStore(t)
	skipWithoutSymlinks(t, st)
	if err := os.MkdirAll(filepath.Join(st.Root, "charts", "record.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "charts", "record.yaml", "tickets.yaml"), []byte("tickets: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(st.Root, "elsewhere"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := putRecord(t, st, TrackerOwner{Chart: "record.yaml"}, "acme/tracking", 1)
	writeRawRecord(t, st, TrackerEntry{ProviderGitHub, "acme/tracking", "2"}, "ticket: ../elsewhere\nrepo: acme/tracking\nissue: 2\n")
	writeRawRecord(t, st, TrackerEntry{ProviderGitHub, "acme/tracking", "3"}, "ticket: JIG-1/x\nrepo: acme/tracking\nissue: 3\n")

	before, _ := os.ReadFile(st.TrackerRecordPath(e))
	_, err := st.LinkTrackerEntry(e, TrackerOwner{Chart: "record.yaml"})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TRACKER_ENTRY_INVALID" {
		t.Errorf("LinkTrackerEntry for a chart named record.yaml = %v, want TRACKER_ENTRY_INVALID", err)
	}
	if after, _ := os.ReadFile(st.TrackerRecordPath(e)); string(after) != string(before) {
		t.Errorf("the record was overwritten:\n%s", after)
	}
	got, err := st.RebuildTrackerLinks()
	if err != nil || got.Changed() {
		t.Fatalf("RebuildTrackerLinks = %+v, %v; want no link made", got, err)
	}
	if links := linkMap(t, st); len(links) != 0 {
		t.Errorf("links = %v, want none", links)
	}
	if _, err := os.Lstat(filepath.Join(st.Root, "trackers", "github", "acme", "elsewhere")); !os.IsNotExist(err) {
		t.Errorf("a link was made outside the entry's folder (lstat err %v)", err)
	}
	problems, err := st.CheckTrackerTree()
	if err != nil {
		t.Fatal(err)
	}
	var orphans int
	for _, p := range problems {
		if p.Code == TrackerRecordOrphan && strings.Contains(p.Msg, "is not a ticket id or a chart name") {
			orphans++
		}
	}
	if orphans != 3 {
		t.Errorf("problems = %+v, want the three ids that cannot name a link reported as naming no ticket or chart", problems)
	}
}
