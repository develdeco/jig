package e2e

// End-to-end tests of the tracker tree (store layout L4): the records a sync
// writes under trackers/<provider>/<scope>/<remote id>/record.yaml, the links
// the store rebuilds from them in the same folder, named by the ticket's id,
// what jig validate says about the tree, and the branch binding that reads it.

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
)

// treeRecordHeader is the line the GitHub mirror starts a record with.
const treeRecordHeader = "# This file is written by jig's GitHub mirror; edits here are overwritten.\n"

// writeTreeRecord writes, as the GitHub mirror does, the record of GitHub issue
// number issue of acme/tracking in the tracker tree, naming ticket (or the
// chart, when chart is set), and returns the entry's folder.
func writeTreeRecord(t *testing.T, fx *fixture.Fixture, owner string, issue int) string {
	t.Helper()
	n := strconv.Itoa(issue)
	dir := filepath.Join(fx.StoreDir, "trackers", "github", "acme", "tracking", n)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	record := treeRecordHeader + owner + "\nrepo: acme/tracking\nissue: " + n + "\nnode_id: I_" + n + "\n"
	writeFileOrFatal(t, filepath.Join(dir, "record.yaml"), []byte(record))
	return dir
}

// storeSymlinks turns git's symlinks on in the fixture store when the machine
// can make one, and skips the test when it cannot. The test git config leaves
// git to probe for them, and git on Windows finds none without its own system
// config.
func storeSymlinks(t *testing.T, fx *fixture.Fixture) {
	t.Helper()
	if err := os.Symlink(".", filepath.Join(t.TempDir(), "probe")); err != nil {
		t.Skipf("this machine cannot make a symlink: %v", err)
	}
	if _, err := gitx.Run(fx.StoreDir, "config", "core.symlinks", "true"); err != nil {
		t.Fatal(err)
	}
}

// treeLinks reads every symlink under the store's trackers/ as its path
// relative to the store, in slash form, to what it points at.
func treeLinks(t *testing.T, fx *fixture.Fixture) map[string]string {
	t.Helper()
	out := map[string]string{}
	root := filepath.Join(fx.StoreDir, "trackers")
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			to, err := os.Readlink(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(fx.StoreDir, p)
			out[filepath.ToSlash(rel)] = filepath.ToSlash(to)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return out
}

// TestTrackerTreeRebuiltFromRecords: the links of the tree are a view of its
// records. A sync writes the records and the store makes every link from them,
// each a relative symlink, named by the ticket's id in its entry's folder, that
// leads to its ticket; jig validate says so when a
// link is missing and points at the sync; and taking every link away and
// syncing again gives the same tree, with no provider anywhere.
func TestTrackerTreeRebuiltFromRecords(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	storeSymlinks(t, fx)
	other := mintTicket(t, env, fx.StoreDir, "Another ticket", "")
	writeTreeRecord(t, fx, "ticket: "+fx.Ticket, 102)
	writeTreeRecord(t, fx, "ticket: "+other, 103)

	// The records are there, the links are not: validate says what to run.
	r := runJig(t, env, fx.StoreDir, "validate", fx.Ticket, "--check")
	if r.Code != 0 || !strings.Contains(r.Stdout, "TRACKER_LINK_WRONG") || !strings.Contains(r.Stdout, "jig trackers sync") {
		t.Fatalf("jig validate with the links missing: exit %d, want 0 with TRACKER_LINK_WRONG pointing at `jig trackers sync`\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}

	want := map[string]string{
		"trackers/github/acme/tracking/102/" + fx.Ticket: "../../../../../tickets/" + fx.Ticket,
		"trackers/github/acme/tracking/103/" + other:     "../../../../../tickets/" + other,
	}
	sync := func() {
		t.Helper()
		if r := runJig(t, env, fx.StoreDir, "trackers", "sync"); r.Code != 0 {
			t.Fatalf("jig trackers sync exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
		}
	}
	sync()
	if got := treeLinks(t, fx); !reflect.DeepEqual(got, want) {
		t.Fatalf("links after a sync = %v, want %v", got, want)
	}
	for link, ticket := range map[string]string{"102": fx.Ticket, "103": other} {
		through, err := os.Stat(filepath.Join(fx.StoreDir, "trackers", "github", "acme", "tracking", link, ticket))
		direct, derr := os.Stat(filepath.Join(fx.StoreDir, "tickets", ticket))
		if err != nil || derr != nil || !os.SameFile(through, direct) {
			t.Errorf("link %s does not lead to %s's folder (%v, %v)", link, ticket, err, derr)
		}
	}
	if r := runJig(t, env, fx.StoreDir, "validate", fx.Ticket, "--check"); r.Code != 0 || strings.Contains(r.Stdout, "TRACKER_") {
		t.Errorf("jig validate after the sync: exit %d, want nothing said of the tree\nstdout:\n%s", r.Code, r.Stdout)
	}

	// Rebuilding from the records alone gives the same tree.
	for link := range want {
		if err := os.Remove(filepath.Join(fx.StoreDir, filepath.FromSlash(link))); err != nil {
			t.Fatal(err)
		}
	}
	if got := treeLinks(t, fx); len(got) != 0 {
		t.Fatalf("links after taking them away = %v, want none", got)
	}
	sync()
	if got := treeLinks(t, fx); !reflect.DeepEqual(got, want) {
		t.Errorf("links rebuilt from the records = %v, want the same %v", got, want)
	}
}

// TestBranchBindsByRemoteIdViaTree: a branch led by the number of a ticket's
// issue binds to the ticket through the record of the tracker tree that names
// it, and names the ticket's own branch the same way; a ticket whose record is
// still in its own folder, where the first mirror kept it, binds too, so a
// store not moved yet works; a number no record holds binds nothing.
func TestBranchBindsByRemoteIdViaTree(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	writeTicketYAML(t, fx, fx.Ticket, "schema_version: 1\ntitle: Upload item gallery\n")
	writeTreeRecord(t, fx, "ticket: "+fx.Ticket, 102)
	old := mintTicket(t, env, fx.StoreDir, "Another ticket", "")
	writeIssueRecord(t, fx, old, 7)
	h := newHarness(t, fx, env, "s-tree")

	if n := nextJSON[nextTicketOut](t, env, fx.RepoDir, fx.Ticket); n.Branch != "102-upload-item-gallery" {
		t.Errorf("jig next: branch = %q, want 102-upload-item-gallery from the record in the tree", n.Branch)
	}
	for branch, want := range map[string]string{
		"102-upload-item-gallery": fx.Ticket,
		"102-something-else":      fx.Ticket,
		"7-another-ticket":        old,
		"JIG-1-upload":            fx.Ticket,
	} {
		h.Branch(branch)
		got := hereJSON(t, env, fx.RepoDir)
		if got.Ticket.ID != want || got.Ticket.How != "branch" {
			t.Errorf("jig here on %s: ticket = %+v, want %s by the branch", branch, got.Ticket, want)
		}
	}
	for _, branch := range []string{"103-no-such-issue", "feature/102-upload"} {
		h.Branch(branch)
		if got := hereJSON(t, env, fx.RepoDir); got.Ticket.ID != "" {
			t.Errorf("jig here on %s: ticket = %+v, want none", branch, got.Ticket)
		}
	}
}
