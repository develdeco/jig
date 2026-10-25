package mirror

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/store"
)

// ownerOfWhat is the owner a report's What names: a ticket id, or
// "charts/<name>" for a chart.
func ownerOfWhat(what string) store.TrackerOwner {
	if name, ok := strings.CutPrefix(what, "charts/"); ok {
		return store.TrackerOwner{Chart: name}
	}
	return store.TrackerOwner{Ticket: what}
}

// treeRecords reads what's GitHub records from the tracker tree as it is on
// disk now.
func treeRecords(t *testing.T, st *store.Store, what string) []store.TrackerRecord {
	t.Helper()
	st.ReloadTrackerTree()
	recs, err := st.TrackerRecordsFor(ownerOfWhat(what))
	if err != nil {
		t.Fatalf("read %s's records from the tracker tree: %v", what, err)
	}
	return recs
}

// hasTreeRecord reports whether what has a record in the tracker tree.
func hasTreeRecord(t *testing.T, st *store.Store, what string) bool {
	t.Helper()
	return len(treeRecords(t, st, what)) > 0
}

// treeRecordPath returns the absolute path of what's one record in the
// tracker tree, failing the test when it has none or more than one.
func treeRecordPath(t *testing.T, st *store.Store, what string) string {
	t.Helper()
	recs := treeRecords(t, st, what)
	if len(recs) != 1 {
		t.Fatalf("%s has %d records in the tracker tree, want exactly 1", what, len(recs))
	}
	return st.TrackerRecordPath(recs[0].Entry)
}

// legacyRecordPath is where A6's mirror first kept what's record: in the
// folder of the ticket or chart itself.
func legacyRecordPath(st *store.Store, what string) string {
	if name, ok := strings.CutPrefix(what, "charts/"); ok {
		return filepath.Join(st.Root, "charts", name, "github.yaml")
	}
	return filepath.Join(st.TicketDir(what), "tracker", "github.yaml")
}

// writeLegacyRecord writes what's record the way a build from before the
// tracker tree did, in the folder of the ticket or chart itself: the bridge's
// own shape, with no ticket: or chart: key.
func writeLegacyRecord(t *testing.T, st *store.Store, what, repo string, issue int, nodeID, item string) {
	t.Helper()
	abs := legacyRecordPath(st, what)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	body := recordHeader + "repo: " + repo + "\nissue: " + itoa(issue) + "\nnode_id: " + nodeID + "\n"
	if item != "" {
		body += "item: " + item + "\n"
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeAdoptedRecord writes what's record in the tracker tree with the
// bridge's own shape (repo, issue, node_id and item - no synced: key at all),
// the form a record jig never wrote carries until this sync adopts it
// (brief.md#Records, #Adoption). item is the board item the bridge had
// already placed the issue on; "" leaves the key out, for a record that
// names an issue never put on a board. It writes the record only: the link is
// the sync's to make.
func writeAdoptedRecord(t *testing.T, st *store.Store, what, repo string, issue int, nodeID, item string) {
	t.Helper()
	var fields yaml.Node
	if err := fields.Encode(githubRecord{Repo: repo, Issue: issue, NodeID: nodeID, Item: item}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteTrackerRecord(githubEntry(repo, issue), ownerOfWhat(what), &fields, recordHeader); err != nil {
		t.Fatal(err)
	}
}

// enableSymlinks turns git's symlinks on in the clone at dir when the machine
// can make one. The test config this package runs under leaves git to probe
// for them, and git on Windows finds none without its own system config, so
// without this a test would only ever see a clone that makes no links. It
// reports whether links are on.
func enableSymlinks(t *testing.T, dir string) bool {
	t.Helper()
	if err := os.Symlink(".", filepath.Join(t.TempDir(), "probe")); err != nil {
		return false
	}
	runGit(t, dir, "config", "core.symlinks", "true")
	return true
}
