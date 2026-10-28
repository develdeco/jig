package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/store"
)

// treeRecordOf returns the text of the record the tracker tree under root holds
// for ticket (trackers/github/<owner>/<repo>/<number>/record.yaml naming it),
// and whether there is one.
func treeRecordOf(t *testing.T, root, ticket string) (string, bool) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, "trackers", "github", "*", "*", "*", "record.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "\nticket: "+ticket+"\n") {
			return string(data), true
		}
	}
	return "", false
}

// putTreeRecord writes the record of GitHub issue number issue of acme/tracking
// for the ticket (or, when chart is set, the chart) in the tracker tree, with
// no link.
func putTreeRecord(t *testing.T, st *store.Store, owner store.TrackerOwner, issue int) {
	t.Helper()
	var fields yaml.Node
	if err := fields.Encode(map[string]any{"repo": "acme/tracking", "issue": issue}); err != nil {
		t.Fatal(err)
	}
	e := store.TrackerEntry{Provider: store.ProviderGitHub, Scope: "acme/tracking", ID: strconv.Itoa(issue)}
	if err := st.WriteTrackerRecord(e, owner, &fields, "# test\n"); err != nil {
		t.Fatal(err)
	}
}

// TestTrackerTreeAdvisoriesSayWhatIsWrongWithTheTree: every kind of thing the
// tree check finds comes out as an advisory under its code, with what to do;
// the tree never refuses a brief.
func TestTrackerTreeAdvisoriesSayWhatIsWrongWithTheTree(t *testing.T) {
	t.Parallel()
	st := planStore(t)
	putTicket(t, st, "T-1", "One", false)
	putTicket(t, st, "T-2", "Two", false)
	putTreeRecord(t, st, store.TrackerOwner{Ticket: "T-1"}, 11)   // no link
	putTreeRecord(t, st, store.TrackerOwner{Ticket: "T-404"}, 12) // no such ticket
	oldRecord := filepath.Join(st.TicketDir("T-2"), "tracker", "github.yaml")
	if err := os.MkdirAll(filepath.Dir(oldRecord), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldRecord, []byte("repo: acme/tracking\nissue: 13\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := map[string]advisory{}
	for _, a := range trackerTreeAdvisories(st) {
		got[a.Code] = a
	}
	for code, help := range map[string]string{
		"TRACKER_LINK_WRONG":     "jig trackers sync",
		"TRACKER_RECORD_ORPHAN":  "ticket: or chart:",
		"TRACKER_RECORD_UNMOVED": "jig trackers sync",
	} {
		a, ok := got[code]
		if !ok {
			t.Errorf("no %s advisory; got %v", code, got)
			continue
		}
		if !strings.Contains(a.Help, help) || a.Message == "" {
			t.Errorf("%s advisory = %+v, want a message and help naming %q", code, a, help)
		}
	}
	if len(got) != 3 {
		t.Errorf("advisories = %v, want exactly the three kinds above", got)
	}
}

// TestTrackerTreeAdvisoriesCapEachKind: a store whose records have all to move
// is told of the first few and how many more there are, not of every one.
func TestTrackerTreeAdvisoriesCapEachKind(t *testing.T) {
	t.Parallel()
	st := planStore(t)
	for i := 1; i <= 5; i++ {
		id := "T-" + strconv.Itoa(i)
		putTicket(t, st, id, "Ticket "+id, false)
		path := filepath.Join(st.TicketDir(id), "tracker", "github.yaml")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("repo: acme/tracking\nissue: "+strconv.Itoa(i)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	as := trackerTreeAdvisories(st)
	if len(as) != trackerTreeAdvisoryMax+1 {
		t.Fatalf("advisories = %+v, want %d and one saying how many more", as, trackerTreeAdvisoryMax)
	}
	if last := as[len(as)-1]; last.Code != "TRACKER_RECORD_UNMOVED" || last.Message != "and 2 more of the same kind" {
		t.Errorf("last advisory = %+v, want it to count the 2 more", last)
	}
}

// TestTrackerTreeAdvisoriesAreSilentForAHealthyStore: no tree, nothing said.
func TestTrackerTreeAdvisoriesAreSilentForAHealthyStore(t *testing.T) {
	t.Parallel()
	st := planStore(t)
	putTicket(t, st, "T-1", "One", false)
	if as := trackerTreeAdvisories(st); len(as) != 0 {
		t.Errorf("advisories = %+v, want none", as)
	}
}
