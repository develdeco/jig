package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
)

// treeHeader is the provider comment a test record starts with.
const treeHeader = "# This file is written by a provider; edits here are overwritten.\n"

// newTreeStore is a standalone store with the tickets JIG-1 and JIG-2 and the
// chart demo, and the warnings it gives collected in the returned list. Its
// git keeps symlinks where the machine can make one: the test config this
// package runs under leaves git to probe for them, and git on Windows finds
// none without its own system config.
func newTreeStore(t *testing.T) (st *Store, warnings *[]string) {
	t.Helper()
	st, work := newTestStandaloneStore(t)
	if err := os.Symlink(".", filepath.Join(t.TempDir(), "probe")); err == nil {
		runGit(t, work, "config", "core.symlinks", "true")
	}
	for _, id := range []string{"JIG-1", "JIG-2"} {
		if err := st.CreateTicketRecord(id, Ticket{Title: "Ticket " + id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(st.Root, "charts", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"), []byte("tickets: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	warnings = new([]string)
	st.Warn = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		*warnings = append(*warnings, fmt.Sprintf(format, args...))
	}
	return st, warnings
}

// skipWithoutSymlinks skips a test that needs this machine to make a symlink
// and git to keep it.
func skipWithoutSymlinks(t *testing.T, st *Store) {
	t.Helper()
	if !st.SymlinksOn() {
		t.Skip("this machine cannot make a symlink")
	}
}

// githubFields is the mapping a GitHub record holds besides its ticket key.
func githubFields(t *testing.T, repo string, issue int) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := n.Encode(struct {
		Repo   string `yaml:"repo"`
		Issue  int    `yaml:"issue"`
		NodeID string `yaml:"node_id"`
	}{repo, issue, "I_" + strconv.Itoa(issue)}); err != nil {
		t.Fatal(err)
	}
	return &n
}

// putRecord writes the record of GitHub issue number issue of repo, for o, and
// returns where it sits.
func putRecord(t *testing.T, st *Store, o TrackerOwner, repo string, issue int) TrackerEntry {
	t.Helper()
	e := TrackerEntry{Provider: ProviderGitHub, Scope: strings.ToLower(repo), ID: strconv.Itoa(issue)}
	if err := st.WriteTrackerRecord(e, o, githubFields(t, repo, issue), treeHeader); err != nil {
		t.Fatal(err)
	}
	return e
}

// linkMap reads every symlink under trackers/ as entry path to target, in
// slash form.
func linkMap(t *testing.T, st *Store) map[string]string {
	t.Helper()
	out := map[string]string{}
	_, entries, err := st.scanTracker()
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range entries {
		abs := filepath.Join(st.Root, filepath.FromSlash(rel))
		if fi, err := os.Lstat(abs); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			got, err := os.Readlink(abs)
			if err != nil {
				t.Fatal(err)
			}
			out[rel] = filepath.ToSlash(got)
		}
	}
	return out
}

// problemCodes lists the codes of problems, in order.
func problemCodes(problems []TrackerProblem) []string {
	var codes []string
	for _, p := range problems {
		codes = append(codes, p.Code)
	}
	return codes
}

// TestTrackerRecordNamesItsOwnerAndKeepsTheProviderKeys: a record is the key
// that names its ticket or chart, then the provider keys exactly as the
// provider wrote them, including one the tree has never heard of; the
// provider header stays the first line of the file.
func TestTrackerRecordNamesItsOwnerAndKeepsTheProviderKeys(t *testing.T) {
	st, _ := newTreeStore(t)
	e := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "7"}
	fields := githubFields(t, "acme/tracking", 7)
	var key, val yaml.Node
	_ = key.Encode("future_key")
	_ = val.Encode([]string{"kept", "as", "is"})
	fields.Content = append(fields.Content, &key, &val)
	if err := st.WriteTrackerRecord(e, TrackerOwner{Ticket: "JIG-1"}, fields, treeHeader); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(st.TrackerRecordPath(e))
	if err != nil {
		t.Fatal(err)
	}
	want := treeHeader + "ticket: JIG-1\nrepo: acme/tracking\nissue: 7\nnode_id: I_7\nfuture_key:\n    - kept\n    - as\n    - is\n"
	if string(data) != want {
		t.Errorf("record =\n%s\nwant\n%s", data, want)
	}

	// Read back, the owner is the tree and the rest is the provider, in
	// order, and the header is gone from it.
	st.ReloadTrackerTree()
	recs, err := st.TrackerRecordsFor(TrackerOwner{Ticket: "JIG-1"})
	if err != nil || len(recs) != 1 {
		t.Fatalf("TrackerRecordsFor(JIG-1) = %+v, %v; want the one record", recs, err)
	}
	if recs[0].Entry != e || recs[0].Ticket != "JIG-1" || recs[0].Chart != "" {
		t.Errorf("record = %+v, want entry %v for JIG-1", recs[0], e)
	}
	var keys []string
	for i := 0; i < len(recs[0].Fields.Content); i += 2 {
		keys = append(keys, recs[0].Fields.Content[i].Value)
	}
	if !reflect.DeepEqual(keys, []string{"repo", "issue", "node_id", "future_key"}) {
		t.Errorf("fields = %v, want the four provider keys in file order", keys)
	}

	// Written again from what was read, it is the same file.
	if err := st.WriteTrackerRecord(e, TrackerOwner{Ticket: "JIG-1"}, &recs[0].Fields, treeHeader); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(st.TrackerRecordPath(e))
	if string(again) != want {
		t.Errorf("record after a read and a write =\n%s\nwant it unchanged", again)
	}

	// A chart record names the chart.
	ce := putRecord(t, st, TrackerOwner{Chart: "demo"}, "acme/tracking", 8)
	cdata, _ := os.ReadFile(st.TrackerRecordPath(ce))
	if !strings.HasPrefix(string(cdata), treeHeader+"chart: demo\n") {
		t.Errorf("chart record = %q, want it to name the chart", cdata)
	}
	if got, _ := st.TrackerRecordsFor(TrackerOwner{Chart: "demo"}); len(got) != 1 || got[0].Entry != ce {
		t.Errorf("TrackerRecordsFor(demo) = %+v, want the chart record", got)
	}
}

// TestTrackerRecordTicketResolvesThroughAliases: a record written under the
// old id of a ticket still belongs to the ticket, as every other ref jig
// follows does.
func TestTrackerRecordTicketResolvesThroughAliases(t *testing.T) {
	st, _ := newTreeStore(t)
	if err := st.CreateTicketRecord("NEW-1", Ticket{Title: "Renamed", Aliases: []string{"OLD-7"}}); err != nil {
		t.Fatal(err)
	}
	e := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "5"}
	if err := st.WriteTrackerRecord(e, TrackerOwner{Ticket: "OLD-7"}, githubFields(t, "acme/tracking", 5), treeHeader); err != nil {
		t.Fatal(err)
	}
	st.ReloadTrackerTree()
	recs, err := st.TrackerRecordsFor(TrackerOwner{Ticket: "NEW-1"})
	if err != nil || len(recs) != 1 {
		t.Fatalf("TrackerRecordsFor(NEW-1) = %+v, %v; want the record written under OLD-7", recs, err)
	}
	if recs[0].Named != "OLD-7" || recs[0].Ticket != "NEW-1" {
		t.Errorf("record names %q and resolves to %q, want OLD-7 and NEW-1", recs[0].Named, recs[0].Ticket)
	}
	if ids, err := st.TrackerIDs("NEW-1"); err != nil || ids.GitHub == nil || ids.GitHub.Number != 5 {
		t.Errorf("TrackerIDs(NEW-1) = %+v, %v; want issue 5", ids, err)
	}
}

// TestTrackerEntryRefusesAPlaceOutsideTheTree: an entry read from a file can
// never lead a write out of trackers/.
func TestTrackerEntryRefusesAPlaceOutsideTheTree(t *testing.T) {
	st, _ := newTreeStore(t)
	for _, e := range []TrackerEntry{
		{Provider: "github", Scope: "../../x", ID: "1"},
		{Provider: "github", Scope: "a/b", ID: "../1"},
		{Provider: "..", Scope: "a/b", ID: "1"},
		{Provider: "github", Scope: "", ID: "1"},
		{Provider: "github", Scope: "a//b", ID: "1"},
		{Provider: "github", Scope: "a/b", ID: "1.yaml"},
		{Provider: "github", Scope: "a/b", ID: "#1"},
	} {
		err := st.WriteTrackerRecord(e, TrackerOwner{Ticket: "JIG-1"}, githubFields(t, "a/b", 1), treeHeader)
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "TRACKER_ENTRY_INVALID" {
			t.Errorf("WriteTrackerRecord(%+v) = %v, want TRACKER_ENTRY_INVALID", e, err)
		}
	}
	if _, err := os.Stat(filepath.Join(st.Root, "trackers")); !os.IsNotExist(err) {
		t.Errorf("a refused entry left trackers/ behind (stat err %v)", err)
	}
}

// TestTrackerRecordIsNeverWrittenOverAnotherOwnersRecord: one remote item is
// one ticket or chart, so a write that would take the record of another is
// refused and leaves it as it was; the owner can rewrite its own.
func TestTrackerRecordIsNeverWrittenOverAnotherOwnersRecord(t *testing.T) {
	st, _ := newTreeStore(t)
	e := putRecord(t, st, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 9)
	before, _ := os.ReadFile(st.TrackerRecordPath(e))

	err := st.WriteTrackerRecord(e, TrackerOwner{Ticket: "JIG-2"}, githubFields(t, "acme/tracking", 9), treeHeader)
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TRACKER_ENTRY_TAKEN" {
		t.Fatalf("WriteTrackerRecord over another ticket's record = %v, want TRACKER_ENTRY_TAKEN", err)
	}
	err = st.WriteTrackerRecord(e, TrackerOwner{Chart: "demo"}, githubFields(t, "acme/tracking", 9), treeHeader)
	if !errors.As(err, &ae) || ae.Code != "TRACKER_ENTRY_TAKEN" {
		t.Fatalf("WriteTrackerRecord over a ticket's record for a chart = %v, want TRACKER_ENTRY_TAKEN", err)
	}
	if after, _ := os.ReadFile(st.TrackerRecordPath(e)); string(after) != string(before) {
		t.Errorf("the record changed:\n%s", after)
	}
	if err := st.WriteTrackerRecord(e, TrackerOwner{Ticket: "JIG-1"}, githubFields(t, "acme/tracking", 9), treeHeader); err != nil {
		t.Errorf("the owner rewriting its own record: %v", err)
	}
}

// TestRemoveTrackerRecordTakesTheLinkAndTheEmptyFoldersToo: the record, its
// link and the scope folders that held only those; a record beside it stays.
func TestRemoveTrackerRecordTakesTheLinkAndTheEmptyFoldersToo(t *testing.T) {
	st, _ := newTreeStore(t)
	a := putRecord(t, st, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 1)
	b := putRecord(t, st, TrackerOwner{Ticket: "JIG-2"}, "acme/other", 2)
	if _, err := st.RebuildTrackerLinks(); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveTrackerRecord(b); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Dir(st.TrackerPath(b))); !os.IsNotExist(err) {
		t.Errorf("the emptied scope folder is still there (lstat err %v)", err)
	}
	if _, err := os.Stat(st.TrackerRecordPath(a)); err != nil {
		t.Errorf("the other record is gone: %v", err)
	}
	if recs, _ := st.TrackerRecords(); len(recs) != 1 || recs[0].Entry != a {
		t.Errorf("records = %+v, want only %v", recs, a)
	}
	if err := st.RemoveTrackerRecord(b); err != nil {
		t.Errorf("removing a record that is already gone: %v", err)
	}
}
