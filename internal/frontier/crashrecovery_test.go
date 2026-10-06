package frontier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/pool"
)

// TestRunRecoversALeaseAnAttemptLeftMidMerge: an attempt that died with a
// merge in progress in the build lease (an API error mid-merge) must not
// stop the next run at acquire: jig aborts the merge, says so, and builds
// (T-35). The merge's own commits were never made, so nothing is lost.
func TestRunRecoversALeaseAnAttemptLeftMidMerge(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)
	branch, err := st.TicketBranch(fx.Ticket, "main")
	if err != nil {
		t.Fatalf("ticket branch: %v", err)
	}
	lease, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, fx.Ticket, pool.Build)
	if err != nil {
		t.Fatalf("acquire the build lease: %v", err)
	}

	// Two sides change the same file, and the merge stops on the conflict,
	// as a builder's would when its session died before resolving it.
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(lease.Dir, "conflict.txt"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		runGitT(t, lease.Dir, "add", "-A")
		if _, err := gitx.RunEnv(lease.Dir, authorEnv, "commit", "-q", "-m", "side: "+content); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	runGitT(t, lease.Dir, "checkout", "-q", "-b", "crash-other")
	write("theirs\n")
	runGitT(t, lease.Dir, "checkout", "-q", branch)
	write("ours\n")
	if _, err := gitx.RunEnv(lease.Dir, authorEnv, "merge", "crash-other"); err == nil {
		t.Fatal("the merge did not stop on its conflict")
	}
	if _, err := os.Stat(filepath.Join(lease.Dir, ".git", "MERGE_HEAD")); err != nil {
		t.Fatalf("no merge in progress after the conflict: %v", err)
	}

	report, err := Run(d, RunOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Run after a crashed merge: %v", err)
	}
	if _, err := os.Stat(filepath.Join(lease.Dir, ".git", "MERGE_HEAD")); !os.IsNotExist(err) {
		t.Errorf("MERGE_HEAD still there after the run (stat err %v), want the merge aborted", err)
	}
	if state, err := st.ReadSliceState(fx.Ticket, "a"); err != nil || state.State != "green" {
		t.Errorf("slice a = %+v (%v), want green: the run builds once the lease is recovered (report %+v)", state, err, report)
	}
	lines, err := journal.Read(st, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	recovered := 0
	for _, l := range lines {
		if l.Event == "lease-recovered" {
			recovered++
			if !strings.Contains(l.Outcome, "merge") {
				t.Errorf("lease-recovered outcome = %q, want it to name the merge", l.Outcome)
			}
		}
	}
	if recovered != 1 {
		t.Errorf("lease-recovered lines = %d, want 1: the first dispatch recovers, later ones find nothing", recovered)
	}
}
