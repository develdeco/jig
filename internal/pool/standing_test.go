package pool

import (
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
)

// TestCompareStartsOneGitProcess: placing a branch against origin's copy is
// one git process whichever way the two stand, the diverged case included (it
// took two ancestry calls), and a copy with no history in common with origin's
// is Diverged too. Not parallel: it counts through the process's trace.
func TestCompareStartsOneGitProcess(t *testing.T) {
	const branch = "add-retry"
	remote := newSourceAndRemote(t)
	pushCommit(t, remote, branch, "a1.txt", "first")
	lease, err := Acquire(t.TempDir(), "fixture", remote, "main", branch, "T-1", Build)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	compare := func(want Standing, what string) {
		t.Helper()
		var got Standing
		n := gittest.CountGit(t, func() {
			var err error
			if got, err = Compare(lease.Dir, branch); err != nil {
				t.Errorf("Compare: %v", err)
			}
		})
		if got != want {
			t.Errorf("%s: Compare = %v, want %v", what, got, want)
		}
		if n != 1 {
			t.Errorf("%s: Compare started %d git processes, want 1", what, n)
		}
	}

	leaseCommit(t, lease.Dir, "own.txt", "jig's own")
	pushCommit(t, remote, branch, "a2.txt", "second")
	run(t, lease.Dir, "fetch", "origin")
	compare(Diverged, "a commit each")

	run(t, lease.Dir, "checkout", "--orphan", "elsewhere")
	writeFile(t, filepath.Join(lease.Dir, "other.txt"), "unrelated")
	run(t, lease.Dir, "add", "-A")
	commit(t, lease.Dir, "an unrelated root")
	run(t, lease.Dir, "update-ref", "refs/heads/"+branch, "HEAD")
	compare(Diverged, "no history in common")
}
