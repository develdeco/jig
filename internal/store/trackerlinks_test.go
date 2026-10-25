package store

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestRebuildTrackerLinksMakesEveryLinkFromTheRecords: each record that names
// a ticket or chart gets a relative symlink to its folder, with no provider
// anywhere; a link that is gone or points elsewhere is made again, a link with
// no record is taken out, and the tree comes out the same every time.
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
		"trackers/github/acme/tracking/11": "../../../../tickets/JIG-1",
		"trackers/github/acme/tracking/12": "../../../../tickets/JIG-2",
		"trackers/github/acme/tracking/13": "../../../../charts/demo",
	}
	if links := linkMap(t, st); !reflect.DeepEqual(links, want) {
		t.Fatalf("links = %v, want %v", links, want)
	}
	// A link is the ticket folder itself, read through it.
	if _, err := os.Stat(filepath.Join(st.Root, "trackers", "github", "acme", "tracking", "11", "ticket.yaml")); err != nil {
		t.Errorf("the link does not lead to the ticket folder: %v", err)
	}

	again, err := st.RebuildTrackerLinks()
	if err != nil || again.Changed() {
		t.Fatalf("a second RebuildTrackerLinks = %+v, %v; want nothing to do", again, err)
	}

	// Break it three ways: a link gone, a link pointing at the wrong ticket,
	// and a link no record is beside.
	root := filepath.Join(st.Root, "trackers", "github", "acme", "tracking")
	if err := os.Remove(filepath.Join(root, "11")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "12")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../../../tickets/JIG-1", filepath.Join(root, "12")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../../../tickets/JIG-2", filepath.Join(root, "99")); err != nil {
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
	standIn := st.TrackerPath(e)
	if err := os.WriteFile(standIn, []byte("../../../../tickets/JIG-1"), 0o644); err != nil {
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
	if data, err := os.ReadFile(standIn); err != nil || string(data) != "../../../../tickets/JIG-1" {
		t.Errorf("the stand-in file = %q, %v; want it left as it is", data, err)
	}
	if recs, _ := st.TrackerRecordsFor(TrackerOwner{Ticket: "JIG-1"}); len(recs) != 1 {
		t.Errorf("records = %+v, want the one record kept", recs)
	}
	if problems, err := st.CheckTrackerTree(); err != nil || len(problems) != 0 {
		t.Errorf("CheckTrackerTree = %+v, %v; want nothing wrong without links to check", problems, err)
	}
}
