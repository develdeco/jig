package pool

import (
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
)

// TestCompareStandsAnUnrelatedHistoryDiverged: a copy and an origin branch
// that share no commit each have commits the other lacks, and are Diverged,
// as are two histories that forked.
func TestCompareStandsAnUnrelatedHistoryDiverged(t *testing.T) {
	t.Parallel()

	const branch = "add-retry"
	remote := newSourceAndRemote(t)
	pushCommit(t, remote, branch, "a1.txt", "first")
	lease, err := Acquire(t.TempDir(), "fixture", remote, "main", branch, "T-1", Build)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	run(t, lease.Dir, "checkout", "--orphan", "elsewhere")
	writeFile(t, filepath.Join(lease.Dir, "other.txt"), "unrelated")
	run(t, lease.Dir, "add", "-A")
	commit(t, lease.Dir, "an unrelated root")
	run(t, lease.Dir, "update-ref", "refs/heads/"+branch, "HEAD")

	got, err := Compare(lease.Dir, branch)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if got != Diverged {
		t.Errorf("a copy with no history in common with origin's stands %v, want Diverged", got)
	}
}

// TestCompareStartsOneGitProcess: placing a branch against origin's copy is
// one git process whichever way the two stand, the diverged case included
// (it took two ancestry calls). Not parallel: it counts through the
// process's trace.
func TestCompareStartsOneGitProcess(t *testing.T) {
	const branch = "add-retry"
	remote := newSourceAndRemote(t)
	pushCommit(t, remote, branch, "a1.txt", "first")
	lease, err := Acquire(t.TempDir(), "fixture", remote, "main", branch, "T-1", Build)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	leaseCommit(t, lease.Dir, "own.txt", "jig's own")
	pushCommit(t, remote, branch, "a2.txt", "second")
	run(t, lease.Dir, "fetch", "origin")

	var got Standing
	n := gittest.CountGit(t, func() {
		var err error
		if got, err = Compare(lease.Dir, branch); err != nil {
			t.Errorf("Compare: %v", err)
		}
	})
	if got != Diverged {
		t.Fatalf("Compare = %v, want Diverged", got)
	}
	if n != 1 {
		t.Errorf("Compare started %d git processes, want 1", n)
	}
}
