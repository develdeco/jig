package store

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeRawRecord writes the record of entry e with exactly text, for a record
// the tree code would never write.
func writeRawRecord(t *testing.T, st *Store, e TrackerEntry, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(st.TrackerRecordPath(e)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.TrackerRecordPath(e), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCheckTrackerTreeIsCleanForAWholeTree: records and links the rebuild made
// have nothing wrong with them.
func TestCheckTrackerTreeIsCleanForAWholeTree(t *testing.T) {
	st, _ := newTreeStore(t)
	skipWithoutSymlinks(t, st)
	putRecord(t, st, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 41)
	putRecord(t, st, TrackerOwner{Chart: "demo"}, "acme/tracking", 42)
	if _, err := st.RebuildTrackerLinks(); err != nil {
		t.Fatal(err)
	}
	if problems, err := st.CheckTrackerTree(); err != nil || len(problems) != 0 {
		t.Fatalf("CheckTrackerTree = %+v, %v; want nothing wrong", problems, err)
	}
	// And a store with no tree at all.
	empty, _ := newTreeStore(t)
	if problems, err := empty.CheckTrackerTree(); err != nil || len(problems) != 0 {
		t.Fatalf("CheckTrackerTree of a store with no trackers/ = %+v, %v", problems, err)
	}
}

// TestCheckTrackerTreeReportsARecordNamingNoTicketOrChart: a record whose
// ticket or chart is not in the store, or that names neither or both.
func TestCheckTrackerTreeReportsARecordNamingNoTicketOrChart(t *testing.T) {
	st, _ := newTreeStore(t)
	putRecord(t, st, TrackerOwner{Ticket: "JIG-9"}, "acme/tracking", 51)
	putRecord(t, st, TrackerOwner{Chart: "nosuch"}, "acme/tracking", 52)
	writeRawRecord(t, st, TrackerEntry{ProviderGitHub, "acme/tracking", "53"}, "repo: acme/tracking\nissue: 53\n")
	writeRawRecord(t, st, TrackerEntry{ProviderGitHub, "acme/tracking", "54"}, "ticket: JIG-1\nchart: demo\nrepo: acme/tracking\nissue: 54\n")

	problems, err := st.CheckTrackerTree()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range problems {
		got = append(got, p.Code+" "+p.Path)
	}
	want := []string{
		"TRACKER_RECORD_ORPHAN trackers/github/acme/tracking/51/record.yaml",
		"TRACKER_RECORD_ORPHAN trackers/github/acme/tracking/52/record.yaml",
		"TRACKER_RECORD_ORPHAN trackers/github/acme/tracking/53/record.yaml",
		"TRACKER_RECORD_ORPHAN trackers/github/acme/tracking/54/record.yaml",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("problems = %v, want %v", got, want)
	}
	if len(problems) > 0 && problems[0].Help == "" {
		t.Errorf("a problem has no help: %+v", problems[0])
	}
}

// TestCheckTrackerTreeReportsTwoRecordsOfOneTicketInOneScope: two issues of
// one repository for the same ticket cannot both be its issue; the same
// ticket in two repositories can.
func TestCheckTrackerTreeReportsTwoRecordsOfOneTicketInOneScope(t *testing.T) {
	st, _ := newTreeStore(t)
	putRecord(t, st, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 61)
	putRecord(t, st, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 62)
	putRecord(t, st, TrackerOwner{Ticket: "JIG-1"}, "acme/elsewhere", 61)
	if _, err := st.RebuildTrackerLinks(); err != nil {
		t.Fatal(err)
	}
	problems, err := st.CheckTrackerTree()
	if err != nil {
		t.Fatal(err)
	}
	if codes := problemCodes(problems); !reflect.DeepEqual(codes, []string{TrackerRecordDuplicate}) {
		t.Fatalf("problems = %+v, want exactly one duplicate", problems)
	}
	if p := problems[0]; p.Path != "trackers/github/acme/tracking/62/record.yaml" {
		t.Errorf("the duplicate is reported at %s, want the second record", p.Path)
	}
}

// TestCheckTrackerTreeReportsARecordWhoseNameIsNotItsRemoteID: a GitHub record
// sits in a folder named for the issue number it holds, in the folder of its
// repository.
func TestCheckTrackerTreeReportsARecordWhoseNameIsNotItsRemoteID(t *testing.T) {
	st, _ := newTreeStore(t)
	e := func(scope, id string) TrackerEntry { return TrackerEntry{ProviderGitHub, scope, id} }
	writeRawRecord(t, st, e("acme/tracking", "7"), "ticket: JIG-1\nrepo: acme/tracking\nissue: 9\n")
	writeRawRecord(t, st, e("acme/tracking", "github"), "ticket: JIG-2\nrepo: acme/tracking\nissue: 10\n")
	writeRawRecord(t, st, e("acme/other", "11"), "chart: demo\nrepo: acme/tracking\nissue: 11\n")
	writeRawRecord(t, st, e("acme/tracking", "12"), "chart: demo\nrepo: ACME/Tracking\nissue: 12\n")
	if _, err := st.RebuildTrackerLinks(); err != nil {
		t.Fatal(err)
	}

	problems, err := st.CheckTrackerTree()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range problems {
		got = append(got, p.Code+" "+p.Path)
	}
	want := []string{
		"TRACKER_RECORD_MISPLACED trackers/github/acme/other/11/record.yaml",
		"TRACKER_RECORD_MISPLACED trackers/github/acme/tracking/7/record.yaml",
		"TRACKER_RECORD_MISPLACED trackers/github/acme/tracking/github/record.yaml",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("problems = %v, want %v (the record of issue 12, whose repo differs by case only, is fine)", got, want)
	}
}

// TestCheckTrackerTreeReportsALinkThatDisagreesWithItsRecord: missing from its
// entry's folder, or pointing at another ticket, or a plain file.
func TestCheckTrackerTreeReportsALinkThatDisagreesWithItsRecord(t *testing.T) {
	st, _ := newTreeStore(t)
	skipWithoutSymlinks(t, st)
	putRecord(t, st, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 71)
	e2 := putRecord(t, st, TrackerOwner{Ticket: "JIG-2"}, "acme/tracking", 72)
	e3 := putRecord(t, st, TrackerOwner{Chart: "demo"}, "acme/tracking", 73)
	if err := os.Symlink("../../../../../tickets/JIG-1", st.TrackerLinkPath(e2, TrackerOwner{Ticket: "JIG-2"})); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.TrackerLinkPath(e3, TrackerOwner{Chart: "demo"}), []byte("../../../../../charts/demo"), 0o644); err != nil {
		t.Fatal(err)
	}
	problems, err := st.CheckTrackerTree()
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, p := range problems {
		if p.Code != TrackerLinkWrong {
			t.Errorf("unexpected problem %+v", p)
		}
		paths = append(paths, p.Path)
		if !strings.Contains(p.Help, "jig trackers sync") {
			t.Errorf("%s: help = %q, want it to point at `jig trackers sync`", p.Path, p.Help)
		}
	}
	want := []string{
		"trackers/github/acme/tracking/71/JIG-1", "trackers/github/acme/tracking/72/JIG-2",
		"trackers/github/acme/tracking/73/demo",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("wrong links = %v, want %v", paths, want)
	}
}

// TestCheckTrackerTreeReportsARecordThatCannotBeDecoded names the file, and
// does not stop the check of the others.
func TestCheckTrackerTreeReportsARecordThatCannotBeDecoded(t *testing.T) {
	st, _ := newTreeStore(t)
	writeRawRecord(t, st, TrackerEntry{ProviderGitHub, "acme/tracking", "81"}, "ticket: [unclosed\n")
	putRecord(t, st, TrackerOwner{Ticket: "JIG-9"}, "acme/tracking", 82)
	problems, err := st.CheckTrackerTree()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{TrackerRecordInvalid, TrackerRecordOrphan}
	if got := problemCodes(problems); !reflect.DeepEqual(got, want) {
		t.Fatalf("problem codes = %v, want %v", got, want)
	}
	if problems[0].Path != "trackers/github/acme/tracking/81/record.yaml" {
		t.Errorf("the invalid record is reported at %s", problems[0].Path)
	}
}
