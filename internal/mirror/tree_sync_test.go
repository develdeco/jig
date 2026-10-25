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

// addDemoChart commits a chart demo whose tickets.yaml is ticketsYAML.
func addDemoChart(t *testing.T, st *store.Store, ticketsYAML string) {
	t.Helper()
	dir := filepath.Join(st.Root, "charts", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tickets.yaml"), []byte(ticketsYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, st.Root, "add", "-A")
	runGit(t, st.Root, "commit", "-m", "add chart")
}

// treeLinks reads every symlink under trackers/ as entry path to the folder it
// leads to, both relative to the store root, in slash form.
func treeLinks(t *testing.T, st *store.Store) map[string]string {
	t.Helper()
	out := map[string]string{}
	root := filepath.Join(st.Root, "trackers")
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink == 0 {
			return nil
		}
		to, err := filepath.EvalSymlinks(p)
		if err != nil {
			return err
		}
		want, err := filepath.EvalSymlinks(st.Root)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(want, to)
		if err != nil {
			return err
		}
		from, _ := filepath.Rel(st.Root, p)
		out[filepath.ToSlash(from)] = filepath.ToSlash(rel)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return out
}

// TestSyncMovesRecordsFromTicketAndChartFoldersIntoTheTree is the move's
// critical path: a store whose records sit in ticket and chart folders syncs
// with each record moved into the tracker tree, naming its ticket or chart and
// keeping every key it had, linked, and every issue updated in place, none
// created; the move goes in with the checkpoint commit that follows.
func TestSyncMovesRecordsFromTicketAndChartFoldersIntoTheTree(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	linksOn := st.SymlinksOn()
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	mintTestTicket(t, st, "DEMO-2", "Second ticket")
	addDemoChart(t, st, "tickets:\n  - id: DEMO-2\n    title: Second ticket\n")
	runGit(t, work, "push", "origin", "main")

	writeLegacyRecord(t, st, "DEMO-1", "example/tracking", 11, "NODE_A", "ITEM_A")
	writeLegacyRecord(t, st, "DEMO-2", "example/tracking", 12, "NODE_B", "")
	writeLegacyRecord(t, st, "charts/demo", "example/tracking", 13, "NODE_C", "")
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "records in the old places")
	runGit(t, work, "push", "origin", "main")

	client := &fakeClient{}
	cfg := loadCfg(t, st)
	report, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{})
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
		"DEMO-1 tickets/DEMO-1/tracker/github.yaml -> trackers/github/example/tracking/11.yaml",
		"DEMO-2 tickets/DEMO-2/tracker/github.yaml -> trackers/github/example/tracking/12.yaml",
		"charts/demo charts/demo/github.yaml -> trackers/github/example/tracking/13.yaml",
	}
	if !reflect.DeepEqual(moved, wantMoved) {
		t.Fatalf("MovedRecords = %v, want %v", moved, wantMoved)
	}

	for what, issue := range map[string]int{"DEMO-1": 11, "DEMO-2": 12, "charts/demo": 13} {
		if _, err := os.Stat(legacyRecordPath(st, what)); !os.IsNotExist(err) {
			t.Errorf("%s: the record is still in its old place (stat err %v)", what, err)
		}
		abs := treeRecordPath(t, st, what)
		data, _ := os.ReadFile(abs)
		key := "ticket: " + what
		if strings.HasPrefix(what, "charts/") {
			key = "chart: demo"
		}
		if !strings.HasPrefix(string(data), recordHeader+key+"\nrepo: example/tracking\nissue: "+itoa(issue)+"\n") {
			t.Errorf("%s's record =\n%s\nwant the header, %q, then its own keys", what, data, key)
		}
		if rec := readTestRecord(t, abs); rec.Issue != issue || rec.Synced == nil || rec.Synced.Title == "" {
			t.Errorf("%s's record = %+v, want issue %d and what this sync wrote", what, rec, issue)
		}
	}
	if rec := readTestRecord(t, treeRecordPath(t, st, "DEMO-1")); rec.Item == "" {
		t.Errorf("DEMO-1's record = %+v, want a board item", rec)
	}
	if linksOn {
		want := map[string]string{
			"trackers/github/example/tracking/11": "tickets/DEMO-1",
			"trackers/github/example/tracking/12": "tickets/DEMO-2",
			"trackers/github/example/tracking/13": "charts/demo",
		}
		if got := treeLinks(t, st); !reflect.DeepEqual(got, want) {
			t.Errorf("links = %v, want %v", got, want)
		}
	}

	// The move is working-tree state until the next checkpoint commits it, as
	// every record change but a creation is.
	if err := st.Push("checkpoint"); err != nil {
		t.Fatal(err)
	}
	if status := runGit(t, work, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Errorf("store dirty after the checkpoint: %q", status)
	}
	tree := runGit(t, work, "ls-tree", "-r", "HEAD")
	for _, old := range []string{"tickets/DEMO-1/tracker/github.yaml", "tickets/DEMO-2/tracker/github.yaml", "charts/demo/github.yaml"} {
		if strings.Contains(tree, old) {
			t.Errorf("the checkpoint still holds %s", old)
		}
	}
	if linksOn && !strings.Contains(tree, "120000 blob") {
		t.Errorf("the checkpoint holds no symlink entry:\n%s", tree)
	}
}

// TestSyncDryRunReadsRecordsInTheOldPlacesAndMovesNone: a dry run finds an
// issue by the record in its ticket folder, reports no creation, and leaves
// that record where it is.
func TestSyncDryRunReadsRecordsInTheOldPlacesAndMovesNone(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	writeLegacyRecord(t, st, "DEMO-1", "example/tracking", 11, "NODE_A", "")
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "records in the old places")

	client := &fakeClient{}
	*client.issue("NODE_A") = fakeIssueState{Title: "First ticket", Body: ticketFooter("DEMO-1"), Open: true}
	report, err := Sync(Deps{Store: st, Cfg: loadCfg(t, st), Client: client}, SyncOpts{DryRun: true})
	if err != nil {
		t.Fatalf("Sync --dry-run: %v", err)
	}
	if len(report.Created) != 0 || len(report.MovedRecords) != 0 {
		t.Errorf("Created %+v, MovedRecords %+v; want none of either", report.Created, report.MovedRecords)
	}
	if _, err := os.Stat(legacyRecordPath(st, "DEMO-1")); err != nil {
		t.Errorf("the old record is gone after a dry run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(st.Root, "trackers")); !os.IsNotExist(err) {
		t.Errorf("a dry run made trackers/ (stat err %v)", err)
	}
	if status := runGit(t, work, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Errorf("a dry run left the store dirty: %q", status)
	}
}

// TestSyncRefusesAnOldPlaceRecordOfAnotherRepoBeforeMovingAnything: the repo
// check that stops a sync of a store whose trackers: entry changed repo runs
// before any record moves.
func TestSyncRefusesAnOldPlaceRecordOfAnotherRepoBeforeMovingAnything(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	mintTestTicket(t, st, "DEMO-2", "Second ticket")
	writeLegacyRecord(t, st, "DEMO-1", "example/tracking", 11, "NODE_A", "")
	writeLegacyRecord(t, st, "DEMO-2", "someone/else", 12, "NODE_B", "")
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "records in the old places")

	_, err := Sync(Deps{Store: st, Cfg: loadCfg(t, st), Client: &fakeClient{}}, SyncOpts{})
	if err == nil || !strings.Contains(err.Error(), "DEMO-2's record names someone/else") {
		t.Fatalf("Sync = %v, want MIRROR_REPO_CHANGED naming DEMO-2", err)
	}
	if _, err := os.Stat(filepath.Join(st.Root, "trackers")); !os.IsNotExist(err) {
		t.Errorf("a refused sync moved a record (stat err %v)", err)
	}
	if _, err := os.Stat(legacyRecordPath(st, "DEMO-1")); err != nil {
		t.Errorf("DEMO-1's old record is gone after a refused sync: %v", err)
	}
}

// TestSyncWithNoTrackerStillMakesTheLinks: the links need no provider, so a
// store with no trackers: entry gets them from its records all the same.
func TestSyncWithNoTrackerStillMakesTheLinks(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	if !st.SymlinksOn() {
		t.Skip("this machine cannot make a symlink")
	}
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	writeAdoptedRecord(t, st, "DEMO-1", "example/tracking", 11, "NODE_A", "")
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "a record")

	report, err := Sync(Deps{Store: st, Cfg: project.Config{}, Client: &fakeClient{}}, SyncOpts{})
	if err != nil || !report.NoTracker {
		t.Fatalf("Sync = %+v, %v; want NoTracker", report, err)
	}
	want := map[string]string{"trackers/github/example/tracking/11": "tickets/DEMO-1"}
	if got := treeLinks(t, st); !reflect.DeepEqual(got, want) {
		t.Errorf("links = %v, want %v", got, want)
	}
}

// TestSyncCommitsARecordAndItsLinkTogether: the claim of a new issue is one
// commit of its record and its link, pushed at once, so the committed tree is
// whole at every step and the next clone has both.
func TestSyncCommitsARecordAndItsLinkTogether(t *testing.T) {
	st, work, remote := newTestRemoteStore(t)
	if !st.SymlinksOn() {
		t.Skip("this machine cannot make a symlink")
	}
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	runGit(t, work, "push", "origin", "main")

	if _, err := Sync(Deps{Store: st, Cfg: loadCfg(t, st), Client: &fakeClient{}}, SyncOpts{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	files := runGit(t, "", "--git-dir", remote, "log", "--name-only", "--format=%s", "-1", "main")
	for _, want := range []string{"DEMO-1: github issue example/tracking#1", "trackers/github/example/tracking/1.yaml", "trackers/github/example/tracking/1\n"} {
		if !strings.Contains(files+"\n", want) {
			t.Errorf("the claim commit =\n%s\nwant it to hold %q", files, want)
		}
	}
	entry := runGit(t, "", "--git-dir", remote, "ls-tree", "main", "trackers/github/example/tracking/1")
	if !strings.HasPrefix(entry, "120000 blob") {
		t.Errorf("the pushed entry = %q, want a symlink (mode 120000)", entry)
	}

	// A fresh clone has the link and the record, and checks clean.
	clone := t.TempDir()
	runGit(t, "", "clone", remote, clone)
	enableSymlinks(t, clone)
	runGit(t, clone, "checkout", "--", "trackers")
	cs, err := store.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	if problems, err := cs.CheckTrackerTree(); err != nil || len(problems) != 0 {
		t.Errorf("CheckTrackerTree of a fresh clone = %+v, %v; want nothing wrong", problems, err)
	}
}

// TestSyncWithoutSymlinksSyncsWithOneWarningAndKeepsEveryRecord: a clone whose
// git keeps no symlinks syncs as usual, warns once however many syncs it
// runs, makes no link, and has every record.
func TestSyncWithoutSymlinksSyncsWithOneWarningAndKeepsEveryRecord(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	runGit(t, work, "config", "core.symlinks", "false")
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	mintTestTicket(t, st, "DEMO-2", "Second ticket")
	runGit(t, work, "push", "origin", "main")
	var warnings []string
	st.Warn = func(format string, args ...any) { warnings = append(warnings, format) }

	client := &fakeClient{}
	cfg := loadCfg(t, st)
	for i := 0; i < 2; i++ {
		if _, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{}); err != nil {
			t.Fatalf("Sync %d: %v", i+1, err)
		}
	}
	if len(client.calls) != 2 {
		t.Errorf("CreateIssue calls = %d, want 2: the sync works the same", len(client.calls))
	}
	for _, what := range []string{"DEMO-1", "DEMO-2"} {
		if !hasTreeRecord(t, st, what) {
			t.Errorf("%s has no record", what)
		}
	}
	if links := treeLinks(t, st); len(links) != 0 {
		t.Errorf("links = %v, want none", links)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "makes no links") {
		t.Errorf("warnings = %q, want exactly one saying this clone makes no links", warnings)
	}
}

// TestSyncMovesARecordWhenItsIssueIsRecreated: an issue gone from GitHub is
// opened again under a new number, and the record moves with it: the old file
// and its link are gone, so the tree holds one record of the ticket.
func TestSyncMovesARecordWhenItsIssueIsRecreated(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	runGit(t, work, "push", "origin", "main")
	client := &fakeClient{}
	cfg := loadCfg(t, st)
	if _, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	old := readTestRecord(t, treeRecordPath(t, st, "DEMO-1"))
	delete(client.issues, old.NodeID)
	client.goneNodeIDs = map[string]bool{old.NodeID: true}

	if _, err := Sync(Deps{Store: st, Cfg: cfg, Client: client}, SyncOpts{}); err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	recs := treeRecords(t, st, "DEMO-1")
	if len(recs) != 1 {
		t.Fatalf("DEMO-1 has %d records after its issue was recreated, want 1", len(recs))
	}
	fresh := readTestRecord(t, st.TrackerRecordPath(recs[0].Entry))
	if fresh.Issue == old.Issue || recs[0].Entry.ID != itoa(fresh.Issue) {
		t.Errorf("record %v holds issue %d (was %d), want it named for the new issue", recs[0].Entry, fresh.Issue, old.Issue)
	}
	if _, err := os.Stat(st.TrackerRecordPath(githubEntry(old.Repo, old.Issue))); !os.IsNotExist(err) {
		t.Errorf("the old record is still there (stat err %v)", err)
	}
	if _, err := os.Lstat(st.TrackerPath(githubEntry(old.Repo, old.Issue))); !os.IsNotExist(err) {
		t.Errorf("the old link is still there (lstat err %v)", err)
	}
	if st.SymlinksOn() {
		want := map[string]string{"trackers/github/example/tracking/" + itoa(fresh.Issue): "tickets/DEMO-1"}
		if got := treeLinks(t, st); !reflect.DeepEqual(got, want) {
			t.Errorf("links = %v, want %v", got, want)
		}
	}
}

// TestSyncRefusesWhileARecordCannotBeRead: a record that cannot be decoded
// names no ticket, so the sync would take its ticket for one with no issue
// and open a second; it stops, naming the file.
func TestSyncRefusesWhileARecordCannotBeRead(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	runGit(t, work, "push", "origin", "main")
	broken := st.TrackerRecordPath(githubEntry("example/tracking", 5))
	if err := os.MkdirAll(filepath.Dir(broken), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte("ticket: [unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	client := &fakeClient{}
	_, err := Sync(Deps{Store: st, Cfg: loadCfg(t, st), Client: client}, SyncOpts{})
	if err == nil || !strings.Contains(err.Error(), "trackers/github/example/tracking/5.yaml could not be read") {
		t.Fatalf("Sync = %v, want a refusal naming the record", err)
	}
	if len(client.calls) != 0 {
		t.Errorf("CreateIssue calls = %+v, want none: no second issue", client.calls)
	}
}
