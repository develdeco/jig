package verifydeliver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
	"github.com/develdeco/jig/internal/pool"
)

// subcommands reduces each of the calls that ran in dir to its git subcommand
// and its first words, the text a test compares ("fetch --update-head-ok",
// "reset --hard").
func subcommands(calls []gittest.GitCall, dir string) []string {
	var out []string
	for _, c := range calls {
		if filepath.Clean(c.Dir) != filepath.Clean(dir) {
			continue
		}
		words := c.Argv[1:]
		for len(words) > 1 && words[0] == "-c" {
			words = words[2:]
		}
		out = append(out, strings.Join(words[:min(len(words), 3)], " "))
	}
	return out
}

// leaseFx is a lease on a branch and a source of that branch with one more
// commit than the lease has.
type leaseFx struct {
	lease, source, branch, tip string
}

// newLeaseFx makes the lease (a clone on branch b) and the repository whose
// copy of b is a commit ahead of it.
func newLeaseFx(t *testing.T) leaseFx {
	t.Helper()
	_, remote := tinyRepo(t)
	lease := cloneFrom(t, remote)
	run(t, lease, "checkout", "-b", "b")
	writeAndCommit(t, lease, "one.txt", "one", "one")
	source := cloneFrom(t, lease)
	tip := writeAndCommit(t, source, "two.txt", "two", "two")
	return leaseFx{lease: lease, source: source, branch: "b", tip: tip}
}

// wantAtTheSourcesTip checks what a round needs of the lease once the copy is
// pointed at: on the branch, at the source's commit, its tree that commit's,
// and nothing dirty.
func (f leaseFx) wantAtTheSourcesTip(t *testing.T) {
	t.Helper()
	if got := run(t, f.lease, "symbolic-ref", "--short", "HEAD"); got != f.branch {
		t.Errorf("the lease is on %q, want %s", got, f.branch)
	}
	if got := run(t, f.lease, "rev-parse", "HEAD"); got != f.tip {
		t.Errorf("the lease is at %s, want the source's %s", got, f.tip)
	}
	if b, err := os.ReadFile(filepath.Join(f.lease, "two.txt")); err != nil || string(b) != "two" {
		t.Errorf("two.txt in the lease = %q, %v; want the source's file", b, err)
	}
	if got := run(t, f.lease, "status", "--porcelain"); got != "" {
		t.Errorf("the lease is dirty:\n%s", got)
	}
}

// TestFetchBranchFromTakesOneProcessWhenTheLeaseIsOnTheBranch: a lease already
// on the branch is fetched with --update-head-ok and left to the reset that
// follows to bring its tree, where a lease that is not steps off, fetches and
// checks the branch out. Either way, once the restore after it has run, the
// lease is at the source's copy. Not parallel: it counts through the
// process's trace.
func TestFetchBranchFromTakesOneProcessWhenTheLeaseIsOnTheBranch(t *testing.T) {
	t.Run("on the branch", func(t *testing.T) {
		f := newLeaseFx(t)
		calls := gittest.GitCalls(t, func() {
			if err := fetchBranchFrom(f.lease, f.source, f.branch); err != nil {
				t.Fatal(err)
			}
		})
		got := subcommands(calls, f.lease)
		if len(got) != 1 || !strings.HasPrefix(got[0], "fetch --update-head-ok") {
			t.Errorf("fetchBranchFrom started %q, want one fetch --update-head-ok", got)
		}
		if err := restorePristine(f.lease, "HEAD", "gate"); err != nil {
			t.Fatal(err)
		}
		f.wantAtTheSourcesTip(t)
	})
	t.Run("not on the branch", func(t *testing.T) {
		f := newLeaseFx(t)
		run(t, f.lease, "checkout", "--detach", "HEAD")
		calls := gittest.GitCalls(t, func() {
			if err := fetchBranchFrom(f.lease, f.source, f.branch); err != nil {
				t.Fatal(err)
			}
		})
		got := subcommands(calls, f.lease)
		if len(got) != 3 || got[0] != "checkout --detach HEAD" || !strings.HasPrefix(got[1], "fetch ") || got[2] != "checkout b" {
			t.Errorf("fetchBranchFrom started %q, want detach, fetch, checkout", got)
		}
		f.wantAtTheSourcesTip(t)
	})
}

// TestFetchTicketBranchFromBuildLeaseTakesOneProcessWhenTheLeaseIsOnTheBranch
// is the same rule for the copy that waits in the build lease. Not parallel.
func TestFetchTicketBranchFromBuildLeaseTakesOneProcessWhenTheLeaseIsOnTheBranch(t *testing.T) {
	setup := func(t *testing.T) (home string, f leaseFx) {
		t.Helper()
		home = t.TempDir()
		_, remote := tinyRepo(t)
		build, err := pool.Dir(home, "repo", "T-1", pool.Build)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(build), 0o755); err != nil {
			t.Fatal(err)
		}
		run(t, filepath.Dir(build), "clone", remote, build)
		run(t, build, "config", "user.name", "jig-fixture")
		run(t, build, "config", "user.email", "fixture@example.invalid")
		run(t, build, "checkout", "-b", "b")
		writeAndCommit(t, build, "one.txt", "one", "one")
		lease := cloneFrom(t, build)
		tip := writeAndCommit(t, build, "two.txt", "two", "two")
		return home, leaseFx{lease: lease, source: build, branch: "b", tip: tip}
	}
	t.Run("on the branch", func(t *testing.T) {
		home, f := setup(t)
		calls := gittest.GitCalls(t, func() {
			if err := fetchTicketBranchFromBuildLease(home, f.lease, "repo", "T-1", f.branch); err != nil {
				t.Fatal(err)
			}
		})
		got := subcommands(calls, f.lease)
		if len(got) != 1 || !strings.HasPrefix(got[0], "fetch --update-head-ok") {
			t.Errorf("fetchTicketBranchFromBuildLease started %q, want one fetch --update-head-ok", got)
		}
		if err := restorePristine(f.lease, "HEAD", "gate"); err != nil {
			t.Fatal(err)
		}
		f.wantAtTheSourcesTip(t)
	})
	t.Run("not on the branch", func(t *testing.T) {
		home, f := setup(t)
		run(t, f.lease, "checkout", "--detach", "HEAD")
		calls := gittest.GitCalls(t, func() {
			if err := fetchTicketBranchFromBuildLease(home, f.lease, "repo", "T-1", f.branch); err != nil {
				t.Fatal(err)
			}
		})
		got := subcommands(calls, f.lease)
		if len(got) != 3 || got[0] != "checkout --detach HEAD" || !strings.HasPrefix(got[1], "fetch ") || got[2] != "checkout b" {
			t.Errorf("fetchTicketBranchFromBuildLease started %q, want detach, fetch, checkout", got)
		}
		f.wantAtTheSourcesTip(t)
	})
}

// TestPrepareReviewRequestResetsALeaseOnlyWhenSomethingMayHaveChangedIt: the
// restore a round starts with is for what the oracles may have left in the
// lease. A round whose lease was restored and had nothing run in it since
// (the gate says so, RoundInput.Pristine) starts no reset; one whose oracle
// ran does. Not parallel.
func TestPrepareReviewRequestResetsALeaseOnlyWhenSomethingMayHaveChangedIt(t *testing.T) {
	for _, c := range []struct {
		name     string
		pristine bool
		resets   int
	}{{"nothing ran in the lease", true, 0}, {"an oracle ran in the lease", false, 1}} {
		t.Run(c.name, func(t *testing.T) {
			dir := newReviewLease(t, "main")
			writeReviewFile(t, dir, "alpha.go", "package alpha\n")
			commitReviewLease(t, dir, "change")
			in := RoundInput{
				Store: newReviewStore(t), Ticket: "JIG-1", Round: 1, LeaseDir: dir,
				RepoName: "fixture-repo", Target: "main", OperatorClone: dir,
				Home: t.TempDir(), UserHome: t.TempDir(), Intent: Intent{Source: IntentSourceBrief, Path: "/x/brief.md"},
				Recorded: true, Pristine: c.pristine,
			}
			var err error
			calls := gittest.GitCalls(t, func() { _, err = PrepareReviewRequest(in) })
			if err != nil {
				t.Fatal(err)
			}
			resets := 0
			for _, s := range subcommands(calls, dir) {
				if strings.HasPrefix(s, "reset --hard") {
					resets++
				}
			}
			if resets != c.resets {
				t.Errorf("PrepareReviewRequest started %d resets of the lease, want %d (%q)", resets, c.resets, subcommands(calls, dir))
			}
		})
	}
}
