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
		if SameDir(filepath.Dir(filepath.FromSlash(strings.TrimSpace(string(b)))), dir) {
			return id
		}
	}
	t.Fatalf("no worktree record names %s: %v", dir, recordIDs(t, repo))
	return ""
}

// TestWorktreeRemoveLeavesTheRecordsGitWould pins what a removal leaves in
// the repository's worktree records, whichever way it finds them: its own
// worktree's record goes, a live worktree's stays, a record whose directory
// was deleted by hand is pruned with it, and one git left locked is kept when
// it is not the removed worktree's.
func TestWorktreeRemoveLeavesTheRecordsGitWould(t *testing.T) {
	t.Parallel()
	add := func(t *testing.T, repo string) string {
		t.Helper()
		dir := filepath.Join(t.TempDir(), "wt")
		if err := WorktreeAddDetached(repo, dir, "HEAD"); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	newRepo := func(t *testing.T) string {
		t.Helper()
		return initRepo(t, filepath.Join(t.TempDir(), "origin.git"))
	}

	t.Run("a registered worktree's record goes and a live one's stays", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		gone, live := add(t, repo), add(t, repo)
		keep := recordOf(t, repo, live)
		if err := WorktreeRemove(repo, gone); err != nil {
			t.Fatal(err)
		}
		if got := recordIDs(t, repo); !reflect.DeepEqual(got, []string{keep}) {
			t.Errorf("records = %v, want only the live worktree's %q", got, keep)
		}
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("the removed directory is there: %v", err)
		}
	})
	t.Run("an unregistered directory leaves the records alone", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		live := add(t, repo)
		before := recordIDs(t, repo)
		stray := filepath.Join(t.TempDir(), "stray")
		if err := os.MkdirAll(stray, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := WorktreeRemove(repo, stray); err != nil {
			t.Fatal(err)
		}
		if got := recordIDs(t, repo); !reflect.DeepEqual(got, before) {
			t.Errorf("records = %v, want %v", got, before)
		}
		if _, err := os.Stat(filepath.Join(live, "f.txt")); err != nil {
			t.Errorf("the live worktree lost its file: %v", err)
		}
	})
	t.Run("a stale record of another worktree is pruned with the removal", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		gone, stale := add(t, repo), add(t, repo)
		if err := os.RemoveAll(stale); err != nil {
			t.Fatal(err)
		}
		if err := WorktreeRemove(repo, gone); err != nil {
			t.Fatal(err)
		}
		if got := recordIDs(t, repo); len(got) != 0 {
			t.Errorf("records = %v, want none: the removed one and the stale one are both gone", got)
		}
	})
	t.Run("a locked worktree's record whose directory is gone is another's and stays", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		gone, locked := add(t, repo), add(t, repo)
		if _, err := Run(repo, "worktree", "lock", "--reason", "mid-add", locked); err != nil {
			t.Fatal(err)
		}
		keep := recordOf(t, repo, locked)
		if err := os.RemoveAll(locked); err != nil {
			t.Fatal(err)
		}
		if err := WorktreeRemove(repo, gone); err != nil {
			t.Fatal(err)
		}
		if got := recordIDs(t, repo); !reflect.DeepEqual(got, []string{keep}) {
			t.Errorf("records = %v, want only the locked %q", got, keep)
		}
	})
}

// TestWorktreeRemoveStartsOnlyTheGitItNeeds: a directory no record names, in
// a repository with no stale record, is deleted with no git process; a
// registered worktree needs one (the prune of its record); a locked one,
// which a prune keeps, needs git's own removal and the prune after it. Not
// parallel: it counts through the process's trace.
func TestWorktreeRemoveStartsOnlyTheGitItNeeds(t *testing.T) {
	repo := initRepo(t, filepath.Join(t.TempDir(), "origin.git"))
	live := filepath.Join(t.TempDir(), "live")
	if err := WorktreeAddDetached(repo, live, "HEAD"); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(t.TempDir(), "stray")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatal(err)
	}
	if n := gittest.CountGit(t, func() {
		if err := WorktreeRemove(repo, stray); err != nil {
			t.Error(err)
		}
	}); n != 0 {
		t.Errorf("removing a directory no record names started %d git processes, want 0", n)
	}
	if n := gittest.CountGit(t, func() {
		if err := WorktreeRemove(repo, filepath.Join(t.TempDir(), "never-was")); err != nil {
			t.Error(err)
		}
	}); n != 0 {
		t.Errorf("removing a directory that is not there started %d git processes, want 0", n)
	}
	if n := gittest.CountGit(t, func() {
		if err := WorktreeRemove(repo, live); err != nil {
			t.Error(err)
		}
	}); n != 1 {
		t.Errorf("removing a registered worktree started %d git processes, want 1 (the prune of its record)", n)
	}
	if got := recordIDs(t, repo); len(got) != 0 {
		t.Errorf("records = %v, want none", got)
	}
}
