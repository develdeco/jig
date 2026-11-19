package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWorktrees lists what the program says about a real repository: its own
// work tree first, a linked worktree with the branch it holds, a detached one
// with none, and one whose directory is gone as prunable.
func TestWorktrees(t *testing.T) {
	t.Parallel()
	repo := initRepo(t, filepath.Join(t.TempDir(), "origin.git"))
	outside := t.TempDir()
	linked := filepath.Join(outside, "repo with space.worktrees", "linked")
	detached := filepath.Join(outside, "detached")
	gone := filepath.Join(outside, "gone")
	mustRun(t, repo, "worktree", "add", "-q", linked, "-b", "feat/t-1")
	mustRun(t, repo, "worktree", "add", "-q", "--detach", detached, "HEAD")
	mustRun(t, repo, "worktree", "add", "-q", gone, "-b", "gone-branch")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	trees, err := Worktrees(repo)
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	byBranch := map[string]Worktree{}
	var detachedTrees []Worktree
	for _, w := range trees {
		if w.Branch == "" {
			detachedTrees = append(detachedTrees, w)
			continue
		}
		byBranch[w.Branch] = w
	}
	if len(trees) != 4 || !trees[0].Main || !SameDir(trees[0].Dir, repo) || trees[0].Branch != "main" || trees[0].Prunable {
		t.Fatalf("Worktrees = %+v, want the repo's own work tree first on main among four", trees)
	}
	if w, ok := byBranch["feat/t-1"]; !ok || w.Main || w.Prunable || !SameDir(w.Dir, linked) {
		t.Errorf("the linked worktree = %+v, %v, want %s on feat/t-1, not main, not prunable", w, ok, linked)
	}
	if w, ok := byBranch["gone-branch"]; !ok || !w.Prunable || w.Main {
		t.Errorf("the worktree whose directory is gone = %+v, %v, want it prunable", w, ok)
	}
	if len(detachedTrees) != 1 || detachedTrees[0].Main || detachedTrees[0].Prunable || !SameDir(detachedTrees[0].Dir, detached) {
		t.Errorf("detached worktrees = %+v, want %s alone", detachedTrees, detached)
	}
}
