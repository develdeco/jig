package mirror

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// writeFlatRecord writes what's record the way the tree kept it before each
// entry got a folder: the record beside its link as <remote id>.yaml, and the
// link named <remote id> (a symlink where the clone keeps them, else the plain
// file git leaves). It writes the pair only; committing them is the caller's.
func writeFlatRecord(t *testing.T, st *store.Store, what, repo string, issue int, nodeID, item string) {
	t.Helper()
	e := githubEntry(repo, issue)
	owner := ownerOfWhat(what)
	key := "ticket: " + what
	target := "../../../../tickets/" + what
	if owner.Chart != "" {
		key = "chart: " + owner.Chart
		target = "../../../../charts/" + owner.Chart
	}
	body := recordHeader + key + "\nrepo: " + repo + "\nissue: " + itoa(issue) + "\nnode_id: " + nodeID + "\n"
	if item != "" {
		body += "item: " + item + "\n"
	}
	flat := st.TrackerPath(e) + ".yaml"
	if err := os.MkdirAll(filepath.Dir(flat), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flat, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	link := st.TrackerPath(e)
	if st.SymlinksOn() {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	} else if err := os.WriteFile(link, []byte(target), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSyncMovesAFlatTreeIntoEntryFolders is the layout change's critical path:
// a store whose tree is still flat, the record beside its link as
// <remote id>.yaml, syncs with each record moved into its entry's folder as
// record.yaml, the old link replaced by the link named by the store id inside
// it, every issue updated in place and none created; the move goes in with the
// checkpoint commit that follows, and the next sync has nothing to move.
func TestSyncMovesAFlatTreeIntoEntryFolders(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	linksOn := st.SymlinksOn()
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	mintTestTicket(t, st, "DEMO-2", "Second ticket")
	addDemoChart(t, st, "tickets:\n  - id: DEMO-2\n    title: Second ticket\n")
	runGit(t, work, "push", "origin", "main")

	writeFlatRecord(t, st, "DEMO-1", "example/tracking", 11, "NODE_A", "ITEM_A")
	writeFlatRecord(t, st, "DEMO-2", "example/tracking", 12, "NODE_B", "")
	writeFlatRecord(t, st, "charts/demo", "example/tracking", 13, "NODE_C", "")
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "a tree in the flat layout")
	runGit(t, work, "push", "origin", "main")

	client := &fakeClient{}
	deps := Deps{Store: st, Cfg: loadCfg(t, st), Client: client}
	report, err := Sync(deps, SyncOpts{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(client.calls) != 0 || len(report.Created) != 0 {
		t.Fatalf("CreateIssue calls %+v, Created %+v; want none: every issue is updated in place", client.calls, report.Created)
	}
	var moved []string
	for _, m := range report.MovedRecords {
		moved = append(moved, m.What+" "+m.From+" -> "+m.To)
	}
	wantMoved := []string{
		"DEMO-1 trackers/github/example/tracking/11.yaml -> trackers/github/example/tracking/11/record.yaml",
		"DEMO-2 trackers/github/example/tracking/12.yaml -> trackers/github/example/tracking/12/record.yaml",
		"charts/demo trackers/github/example/tracking/13.yaml -> trackers/github/example/tracking/13/record.yaml",
	}
	if !reflect.DeepEqual(moved, wantMoved) {
		t.Fatalf("MovedRecords = %v, want %v", moved, wantMoved)
	}

	for what, issue := range map[string]int{"DEMO-1": 11, "DEMO-2": 12, "charts/demo": 13} {
		flat := filepath.Join(st.Root, "trackers", "github", "example", "tracking", itoa(issue)+".yaml")
		if _, err := os.Stat(flat); !os.IsNotExist(err) {
			t.Errorf("%s: the flat record is still there (stat err %v)", what, err)
		}
		abs := treeRecordPath(t, st, what)
		if want := filepath.Join(st.Root, "trackers", "github", "example", "tracking", itoa(issue), "record.yaml"); abs != want {
			t.Errorf("%s's record = %s, want %s", what, abs, want)
		}
		data, _ := os.ReadFile(abs)
		key := "ticket: " + what
		if strings.HasPrefix(what, "charts/") {
			key = "chart: demo"
		}
		if !strings.HasPrefix(string(data), recordHeader+key+"\nrepo: example/tracking\nissue: "+itoa(issue)+"\nnode_id: ") {
			t.Errorf("%s's record =\n%s\nwant the header, %q, then its own keys", what, data, key)
		}
		if rec := readTestRecord(t, abs); rec.Issue != issue || rec.Synced == nil || rec.Synced.Title == "" {
			t.Errorf("%s's record = %+v, want issue %d and what this sync wrote", what, rec, issue)
		}
	}
	if linksOn {
		want := map[string]string{
			"trackers/github/example/tracking/11/DEMO-1": "tickets/DEMO-1",
			"trackers/github/example/tracking/12/DEMO-2": "tickets/DEMO-2",
			"trackers/github/example/tracking/13/demo":   "charts/demo",
		}
		if got := treeLinks(t, st); !reflect.DeepEqual(got, want) {
			t.Errorf("links = %v, want %v", got, want)
		}
	}
	if problems, err := st.CheckTrackerTree(); err != nil || len(problems) != 0 {
		t.Errorf("CheckTrackerTree = %+v, %v; want nothing wrong", problems, err)
	}

	if err := st.Push("checkpoint"); err != nil {
		t.Fatal(err)
	}
	if status := runGit(t, work, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Errorf("store dirty after the checkpoint: %q", status)
	}
	tree := runGit(t, work, "ls-tree", "-r", "HEAD", "--name-only")
	for _, old := range []string{"tracking/11.yaml", "tracking/12.yaml", "tracking/13.yaml"} {
		if strings.Contains(tree, old) {
			t.Errorf("the checkpoint still holds %s", old)
		}
	}
	if entry := runGit(t, work, "ls-tree", "HEAD", "trackers/github/example/tracking/11"); !strings.HasPrefix(entry, "040000 tree") {
		t.Errorf("trackers/github/example/tracking/11 in the checkpoint = %q, want a folder, not the old link", entry)
	}

	again, err := Sync(deps, SyncOpts{})
	if err != nil || len(again.MovedRecords) != 0 || len(client.calls) != 0 {
		t.Errorf("second Sync = %+v, %v, CreateIssue calls %+v; want nothing moved and none created", again.MovedRecords, err, client.calls)
	}
}

// TestSyncMovesAFlatTreeEvenWithNoTracker: moving a record and making its link
// need no provider, so a store with no trackers: entry gets its tree in
// folders from its records all the same.
func TestSyncMovesAFlatTreeEvenWithNoTracker(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	writeFlatRecord(t, st, "DEMO-1", "example/tracking", 11, "NODE_A", "")
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "a tree in the flat layout")

	report, err := Sync(Deps{Store: st, Cfg: project.Config{}, Client: &fakeClient{}}, SyncOpts{})
	if err != nil || !report.NoTracker {
		t.Fatalf("Sync = %+v, %v; want NoTracker", report, err)
	}
	if _, err := os.Stat(st.TrackerRecordPath(githubEntry("example/tracking", 11))); err != nil {
		t.Errorf("the record is not in its entry's folder: %v", err)
	}
	if st.SymlinksOn() {
		want := map[string]string{"trackers/github/example/tracking/11/DEMO-1": "tickets/DEMO-1"}
		if got := treeLinks(t, st); !reflect.DeepEqual(got, want) {
			t.Errorf("links = %v, want %v", got, want)
		}
	}
}

// TestSyncDryRunReadsAFlatTreeAndMovesNone: a dry run finds an issue by the
// record beside its link, reports no creation, and leaves the tree as it is.
func TestSyncDryRunReadsAFlatTreeAndMovesNone(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	writeFlatRecord(t, st, "DEMO-1", "example/tracking", 11, "NODE_A", "")
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "a tree in the flat layout")

	client := &fakeClient{}
	*client.issue("NODE_A") = fakeIssueState{Title: "First ticket", Body: ticketFooter("DEMO-1"), Open: true}
	report, err := Sync(Deps{Store: st, Cfg: loadCfg(t, st), Client: client}, SyncOpts{DryRun: true})
	if err != nil {
		t.Fatalf("Sync --dry-run: %v", err)
	}
	if len(report.Created) != 0 || len(report.MovedRecords) != 0 {
		t.Errorf("Created %+v, MovedRecords %+v; want none of either", report.Created, report.MovedRecords)
	}
	if _, err := os.Stat(filepath.Join(st.Root, "trackers", "github", "example", "tracking", "11.yaml")); err != nil {
		t.Errorf("the flat record is gone after a dry run: %v", err)
	}
	if status := runGit(t, work, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Errorf("a dry run left the store dirty: %q", status)
	}
}

// TestSyncRefusalNamesTheFlatRecordItCannotRead: a record beside its link that
// cannot be decoded stops the sync, and the refusal names that file, not the
// folder path it would move to, which does not exist.
func TestSyncRefusalNamesTheFlatRecordItCannotRead(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	runGit(t, work, "push", "origin", "main")
	broken := st.TrackerPath(githubEntry("example/tracking", 5)) + ".yaml"
	if err := os.MkdirAll(filepath.Dir(broken), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte("ticket: [unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	client := &fakeClient{}
	_, err := Sync(Deps{Store: st, Cfg: loadCfg(t, st), Client: client}, SyncOpts{})
	if err == nil || !strings.Contains(err.Error(), "trackers/github/example/tracking/5.yaml could not be read") {
		t.Fatalf("Sync = %v, want a refusal naming the flat file", err)
	}
	if len(client.calls) != 0 {
		t.Errorf("CreateIssue calls = %+v, want none: no second issue", client.calls)
	}
}

// TestSyncRenamesTheLinkWhenATicketIsRenamed: a ticket renamed through an alias
// keeps its issue, found by the record that still names the old id, and the
// next sync names the link by the id the ticket has now.
func TestSyncRenamesTheLinkWhenATicketIsRenamed(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	if !st.SymlinksOn() {
		t.Skip("this machine cannot make a symlink")
	}
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	runGit(t, work, "push", "origin", "main")
	client := &fakeClient{}
	deps := Deps{Store: st, Cfg: loadCfg(t, st), Client: client}
	if _, err := Sync(deps, SyncOpts{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	if got, want := treeLinks(t, st), map[string]string{"trackers/github/example/tracking/1/DEMO-1": "tickets/DEMO-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("links after the first sync = %v, want %v", got, want)
	}

	if err := os.RemoveAll(st.TicketDir("DEMO-1")); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTicketRecord("DEMO-9", store.Ticket{Title: "First ticket", Aliases: []string{"DEMO-1"}}); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "DEMO-1 is now DEMO-9")

	report, err := Sync(deps, SyncOpts{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if len(client.calls) != 1 || len(report.Created) != 0 {
		t.Errorf("CreateIssue calls %+v, Created %+v; want no second issue for the renamed ticket", client.calls, report.Created)
	}
	if got, want := treeLinks(t, st), map[string]string{"trackers/github/example/tracking/1/DEMO-9": "tickets/DEMO-9"}; !reflect.DeepEqual(got, want) {
		t.Errorf("links after the rename = %v, want %v", got, want)
	}
	if problems, err := st.CheckTrackerTree(); err != nil || len(problems) != 0 {
		t.Errorf("CheckTrackerTree = %+v, %v; want nothing wrong", problems, err)
	}
}
