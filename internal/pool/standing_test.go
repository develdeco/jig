package pool

import (
	"path/filepath"
	"testing"
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
