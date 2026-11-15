package gitx

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
)

// worktreeStream is one commit on main, the repository the removals below are
// made in.
const worktreeStream = `commit refs/heads/main
committer t <t@example.invalid> 1700000000 +0000
data 4
base
M 100644 inline f.txt
data 2
f

`

// recordIDs lists the names of repo's worktree records.
func recordIDs(t *testing.T, repo string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(repo, ".git", "worktrees"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.Name())
	}
	sort.Strings(ids)
	return ids
}

// recordOf is the name of the record of the worktree at dir.
func recordOf(t *testing.T, repo, dir string) string {
	t.Helper()
	for _, id := range recordIDs(t, repo) {
		b, err := os.ReadFile(filepath.Join(repo, ".git", "worktrees", id, "gitdir"))
		if err != nil {
			continue
		}
		target := filepath.FromSlash(strings.TrimSpace(string(b)))
		if !filepath.IsAbs(target) {
			target = filepath.Join(repo, ".git", "worktrees", id, target)
		}
		if SameDir(filepath.Dir(target), dir) {
			return id
		}
	}
	t.Fatalf("no worktree record names %s: %v", dir, recordIDs(t, repo))
	return ""
}

// TestWorktreeRemoveLeavesTheRecordsGitWouldAndStartsOnlyTheGitItNeeds walks
// one repository through the removals a verification makes and pins, for each,
// the records it leaves and the git processes it starts: a registered
// worktree's record goes (one prune) and a live one's stays; a directory no
// record names, and one that is not there, are deleted or left with no git
// process; a stale record of another worktree is pruned with the removal; a
// locked record whose directory is gone is another's and stays until it is
// the removal's own, which git's forced removal takes (one process). The steps
// share the repository, so each starts from what the one before left. Not
// parallel: it counts through the process's trace.
func TestWorktreeRemoveLeavesTheRecordsGitWouldAndStartsOnlyTheGitItNeeds(t *testing.T) {
	repo := copyRepo(t, streamRepo(t, "worktrees", worktreeStream))
	add := func() string {
		t.Helper()
		dir := filepath.Join(t.TempDir(), "wt")
		if err := WorktreeAddDetached(repo, dir, "HEAD"); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	remove := func(dir string) int {
		t.Helper()
		return gittest.CountGit(t, func() {
			if err := WorktreeRemove(repo, dir); err != nil {
				t.Fatal(err)
			}
		})
	}
	wantRecords := func(what string, want ...string) {
		t.Helper()
		got := recordIDs(t, repo)
		if len(want) == 0 {
			want = nil
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: records = %v, want %v", what, got, want)
		}
	}
	wantProcesses := func(what string, got, want int) {
		t.Helper()
		if got != want {
			t.Errorf("%s started %d git processes, want %d", what, got, want)
		}
	}

	// A registered worktree goes with its record, a live one's stays.
	gone, live := add(), add()
	keep := recordOf(t, repo, live)
	wantProcesses("removing a registered worktree", remove(gone), 1)
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Errorf("the removed directory is there: %v", err)
	}
	wantRecords("after the registered one went", keep)

	// A directory no record names is deleted, and a directory that is not there
	// is no error; neither starts git, and the records stay as they were.
	stray := filepath.Join(t.TempDir(), "stray")
	if err := os.MkdirAll(filepath.Join(stray, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	wantProcesses("removing a directory no record names", remove(stray), 0)
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Errorf("the stray directory is there: %v", err)
	}
	wantProcesses("removing a directory that is not there", remove(filepath.Join(t.TempDir(), "never-was")), 0)
	wantRecords("after the directories no record names", keep)
	if _, err := os.Stat(filepath.Join(live, "f.txt")); err != nil {
		t.Errorf("the live worktree lost its file: %v", err)
	}

	// A stale record of another worktree (its directory deleted by hand) is
	// pruned with the removal.
	stale := add()
	if err := os.RemoveAll(stale); err != nil {
		t.Fatal(err)
	}
	wantProcesses("removing a worktree beside a stale record", remove(live), 1)
	wantRecords("after the removal that pruned the stale record")

	// A locked record whose directory is gone is another worktree's: a prune
	// keeps it, and so does the removal of that other. The removal of its own
	// is git's forced removal, and nothing is left to prune.
	locked, other := add(), add()
	if _, err := Run(repo, "worktree", "lock", "--reason", "mid-add", locked); err != nil {
		t.Fatal(err)
	}
	lockedID := recordOf(t, repo, locked)
	if err := os.RemoveAll(locked); err != nil {
		t.Fatal(err)
	}
	wantProcesses("removing a worktree beside a locked record", remove(other), 1)
	wantRecords("after the removal beside a locked record", lockedID)
	wantProcesses("removing the locked worktree", remove(locked), 1)
	wantRecords("after the locked worktree's own removal")

	// With worktree.useRelativePaths git writes a record's gitdir relative to
	// the record. A locked record of the removed directory (an add that died
	// half way) still takes git's forced removal, so the directory can be
	// added again.
	if _, err := Run(repo, "config", "worktree.useRelativePaths", "true"); err != nil {
		t.Fatal(err)
	}
	rel := add()
	relID := recordOf(t, repo, rel)
	if b, err := os.ReadFile(filepath.Join(repo, ".git", "worktrees", relID, "gitdir")); err != nil || filepath.IsAbs(filepath.FromSlash(strings.TrimSpace(string(b)))) {
		t.Skipf("this git writes absolute gitdir records (%q, %v)", b, err)
	}
	if _, err := Run(repo, "worktree", "lock", "--reason", "initializing", rel); err != nil {
		t.Fatal(err)
	}
	wantProcesses("removing a locked worktree with a relative record", remove(rel), 1)
	wantRecords("after the relative locked worktree's removal")
	if err := WorktreeAddDetached(repo, rel, "HEAD"); err != nil {
		t.Errorf("adding at the directory again: %v", err)
	}
}
