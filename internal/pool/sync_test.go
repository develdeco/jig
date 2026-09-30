package pool

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// pushCommit plays a second contributor: a fresh clone of remote, one commit
// writing file, pushed to remote under branch - continuing the branch when
// remote already has it, starting it from main when not. It returns the
// pushed commit.
func pushCommit(t *testing.T, remote, branch, file, content string) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "clone", remote, ".")
	if out := run(t, dir, "ls-remote", "--heads", "origin", branch); out != "" {
		run(t, dir, "checkout", branch)
	} else {
		run(t, dir, "checkout", "-b", branch)
	}
	writeFile(t, filepath.Join(dir, file), content)
	run(t, dir, "add", "-A")
	commit(t, dir, "add "+file)
	run(t, dir, "push", "origin", branch)
	return run(t, dir, "rev-parse", "HEAD")
}

// leaseCommit commits one file in a lease and returns the commit.
func leaseCommit(t *testing.T, dir, file, content string) string {
	t.Helper()
	writeFile(t, filepath.Join(dir, file), content)
	run(t, dir, "add", "-A")
	commit(t, dir, "add "+file)
	return run(t, dir, "rev-parse", "HEAD")
}

// TestAcquireSyncsABranchWithOrigin covers the sync rule for a branch that
// exists on origin, for every role that acquires a lease: the branch is
// fast-forwarded when the lease has no commits of its own, kept when the
// lease is ahead, left alone when equal, and refused with BRANCH_DIVERGED
// when each side has commits the other lacks - never merged, reset or
// rebased.
func TestAcquireSyncsABranchWithOrigin(t *testing.T) {
	t.Parallel()

	const branch = "add-retry"
	for _, role := range []Role{Build, Gate, Publish} {
		role := role
		t.Run(role.String(), func(t *testing.T) {
			t.Parallel()

			// setup leaves the lease on the author's first commit and returns
			// the jig home, the remote, the lease and that commit.
			setup := func(t *testing.T) (jigHome, remote string, lease Lease, first string) {
				t.Helper()
				jigHome = t.TempDir()
				remote = newSourceAndRemote(t)
				first = pushCommit(t, remote, branch, "a1.txt", "first")
				lease, err := Acquire(jigHome, "fixture", remote, "main", branch, "T-1", role)
				if err != nil {
					t.Fatalf("first Acquire: %v", err)
				}
				if head := run(t, lease.Dir, "rev-parse", "HEAD"); head != first {
					t.Fatalf("a lease of a branch that exists on origin starts at %s, want origin's tip %s", head, first)
				}
				return jigHome, remote, lease, first
			}

			t.Run("origin ahead is fast-forwarded", func(t *testing.T) {
				t.Parallel()
				jigHome, remote, lease, _ := setup(t)
				second := pushCommit(t, remote, branch, "a2.txt", "second")

				again, err := Acquire(jigHome, "fixture", remote, "main", branch, "T-1", role)
				if err != nil {
					t.Fatalf("Acquire after the author pushed: %v", err)
				}
				if again.Dir != lease.Dir {
					t.Fatalf("Acquire dir = %q, want reuse of %q", again.Dir, lease.Dir)
				}
				if head := run(t, lease.Dir, "rev-parse", "HEAD"); head != second {
					t.Fatalf("lease HEAD = %s, want the author's new tip %s", head, second)
				}
				if got := run(t, lease.Dir, "symbolic-ref", "--short", "HEAD"); got != branch {
					t.Fatalf("lease is on %q, want %q checked out", got, branch)
				}
				if _, err := os.Stat(filepath.Join(lease.Dir, "a2.txt")); err != nil {
					t.Fatalf("the fast-forward did not update the worktree: %v", err)
				}
			})

			t.Run("lease ahead is kept", func(t *testing.T) {
				t.Parallel()
				jigHome, remote, lease, first := setup(t)
				own := leaseCommit(t, lease.Dir, "own.txt", "jig's own")

				if _, err := Acquire(jigHome, "fixture", remote, "main", branch, "T-1", role); err != nil {
					t.Fatalf("Acquire with unpushed commits of its own: %v", err)
				}
				if head := run(t, lease.Dir, "rev-parse", "HEAD"); head != own {
					t.Fatalf("lease HEAD = %s, want its own commit %s kept", head, own)
				}
				if got := run(t, remote, "rev-parse", "refs/heads/"+branch); got != first {
					t.Fatalf("origin's %s = %s, want %s: Acquire must not push", branch, got, first)
				}
			})

			t.Run("equal is left alone", func(t *testing.T) {
				t.Parallel()
				jigHome, remote, lease, first := setup(t)

				if _, err := Acquire(jigHome, "fixture", remote, "main", branch, "T-1", role); err != nil {
					t.Fatalf("Acquire: %v", err)
				}
				if head := run(t, lease.Dir, "rev-parse", "HEAD"); head != first {
					t.Fatalf("lease HEAD = %s, want %s", head, first)
				}
			})

			t.Run("diverged is refused and left alone", func(t *testing.T) {
				t.Parallel()
				jigHome, remote, lease, _ := setup(t)
				own := leaseCommit(t, lease.Dir, "own.txt", "jig's own")
				theirs := pushCommit(t, remote, branch, "a2.txt", "second")

				_, err := Acquire(jigHome, "fixture", remote, "main", branch, "T-1", role)
				var ae *axi.Error
				if !errors.As(err, &ae) || ae.Code != "BRANCH_DIVERGED" {
					t.Fatalf("Acquire over a diverged branch: err = %v, want an *axi.Error BRANCH_DIVERGED", err)
				}
				for _, want := range []string{branch, lease.Dir, "1 commit(s) origin/" + branch + " lacks", "has 1 the lease lacks"} {
					if !strings.Contains(ae.Msg, want) {
						t.Errorf("BRANCH_DIVERGED message %q does not name %q", ae.Msg, want)
					}
				}
				if len(ae.Help) == 0 {
					t.Error("BRANCH_DIVERGED carries no help")
				}
				if head := run(t, lease.Dir, "rev-parse", "HEAD"); head != own {
					t.Fatalf("lease HEAD = %s after the refusal, want its own commit %s untouched", head, own)
				}
				if got := run(t, remote, "rev-parse", "refs/heads/"+branch); got != theirs {
					t.Fatalf("origin's %s = %s, want the author's %s untouched", branch, got, theirs)
				}
				if status := run(t, lease.Dir, "status", "--porcelain"); status != "" {
					t.Fatalf("lease left dirty by the refusal: %q", status)
				}
			})
		})
	}
}

// TestAcquireFastForwardFailsLoudlyOverALocalEdit: the fast-forward is git's
// own `merge --ff-only`, so an uncommitted edit to a file the author's new
// commits also change stops it with git's error. The acquire fails, the
// branch keeps its commit, and the edit survives - nothing is discarded to
// make room.
func TestAcquireFastForwardFailsLoudlyOverALocalEdit(t *testing.T) {
	t.Parallel()

	jigHome := t.TempDir()
	remote := newSourceAndRemote(t)
	first := pushCommit(t, remote, "add-retry", "a1.txt", "first")
	lease, err := Acquire(jigHome, "fixture", remote, "main", "add-retry", "T-1", Build)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	writeFile(t, filepath.Join(lease.Dir, "a1.txt"), "edited in the lease")
	pushCommit(t, remote, "add-retry", "a1.txt", "the author changed it too")

	_, err = Acquire(jigHome, "fixture", remote, "main", "add-retry", "T-1", Build)
	if err == nil {
		t.Fatal("Acquire fast-forwarded over an uncommitted edit of a file the new commits change")
	}
	var ae *axi.Error
	if errors.As(err, &ae) {
		t.Fatalf("err = %v, want git's own failure, not an axi refusal", err)
	}
	if head := run(t, lease.Dir, "rev-parse", "HEAD"); head != first {
		t.Fatalf("lease HEAD = %s, want %s: the branch must not move", head, first)
	}
	if got, err := os.ReadFile(filepath.Join(lease.Dir, "a1.txt")); err != nil || string(got) != "edited in the lease" {
		t.Fatalf("the lease's edit = %q (err %v), want it kept", got, err)
	}
}

// TestAcquireLeavesABranchOriginLacksAlone: a branch that does not exist on
// origin - the normal jig/<ticket> - is nobody else's to sync with. Whatever
// origin's other branches do, the lease's own commits stay and nothing is
// refused.
func TestAcquireLeavesABranchOriginLacksAlone(t *testing.T) {
	t.Parallel()

	jigHome := t.TempDir()
	remote := newSourceAndRemote(t)
	lease, err := Acquire(jigHome, "fixture", remote, "main", "jig/T-1", "T-1", Build)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	own := leaseCommit(t, lease.Dir, "own.txt", "jig's own")
	pushCommit(t, remote, "main", "moved.txt", "main moved")

	if _, err := Acquire(jigHome, "fixture", remote, "main", "jig/T-1", "T-1", Build); err != nil {
		t.Fatalf("Acquire after main moved: %v", err)
	}
	if head := run(t, lease.Dir, "rev-parse", "HEAD"); head != own {
		t.Fatalf("lease HEAD = %s, want its own commit %s kept", head, own)
	}
	if _, err := os.Stat(filepath.Join(lease.Dir, "moved.txt")); !os.IsNotExist(err) {
		t.Fatalf("the branch took main's new file (stat err %v): a branch origin lacks must not be synced", err)
	}
}

// TestCompareSaysHowACopyStandsAgainstOrigin: Compare is the one rule the
// sync and the gate's choice of copy both read, so each of its four answers is
// pinned.
func TestCompareSaysHowACopyStandsAgainstOrigin(t *testing.T) {
	t.Parallel()

	const branch = "add-retry"
	remote := newSourceAndRemote(t)
	pushCommit(t, remote, branch, "a1.txt", "first")
	lease, err := Acquire(t.TempDir(), "fixture", remote, "main", branch, "T-1", Build)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	stand := func() Standing {
		t.Helper()
		got, err := Compare(lease.Dir, branch)
		if err != nil {
			t.Fatalf("Compare: %v", err)
		}
		return got
	}

	if got := stand(); got != InStep {
		t.Fatalf("a lease on origin's tip stands %v, want InStep", got)
	}

	pushCommit(t, remote, branch, "a2.txt", "second")
	run(t, lease.Dir, "fetch", "origin")
	if got := stand(); got != Behind {
		t.Fatalf("a lease with origin's new commit unfetched into it stands %v, want Behind", got)
	}

	run(t, lease.Dir, "merge", "--ff-only", "origin/"+branch)
	leaseCommit(t, lease.Dir, "own.txt", "jig's own")
	if got := stand(); got != Ahead {
		t.Fatalf("a lease with a commit origin lacks stands %v, want Ahead", got)
	}

	pushCommit(t, remote, branch, "a3.txt", "third")
	run(t, lease.Dir, "fetch", "origin")
	if got := stand(); got != Diverged {
		t.Fatalf("a lease and origin with a commit each stand %v, want Diverged", got)
	}
}

// TestAcquireMustExistOnOrigin: a caller whose branch was recorded because it
// is on origin refuses, BRANCH_NOT_FOUND, instead of having Acquire cut the
// branch from the target - on a fresh lease, and on one that fetched the
// branch before origin lost it. Without the option nothing changes: the
// ordinary jig/<ticket> is cut from the target.
func TestAcquireMustExistOnOrigin(t *testing.T) {
	t.Parallel()

	const branch = "add-retry"

	t.Run("a branch origin lacks is refused and not cut", func(t *testing.T) {
		t.Parallel()
		jigHome := t.TempDir()
		remote := newSourceAndRemote(t)
		_, err := Acquire(jigHome, "fixture", remote, "main", branch, "T-1", Build, MustExistOnOrigin())
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "BRANCH_NOT_FOUND" || len(ae.Help) == 0 {
			t.Fatalf("Acquire of a branch origin lacks: err = %v, want an *axi.Error BRANCH_NOT_FOUND with help", err)
		}
		dir, derr := Dir(jigHome, "fixture", "T-1", Build)
		if derr != nil {
			t.Fatal(derr)
		}
		if refExists(dir, "refs/heads/"+branch) {
			t.Fatal("the refused Acquire still cut the branch from the target")
		}
	})

	t.Run("a branch origin has is acquired", func(t *testing.T) {
		t.Parallel()
		remote := newSourceAndRemote(t)
		tip := pushCommit(t, remote, branch, "a1.txt", "first")
		lease, err := Acquire(t.TempDir(), "fixture", remote, "main", branch, "T-1", Build, MustExistOnOrigin())
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		if head := run(t, lease.Dir, "rev-parse", "HEAD"); head != tip {
			t.Fatalf("lease HEAD = %s, want origin's tip %s", head, tip)
		}
	})

	t.Run("a branch origin lost since the last acquire is refused", func(t *testing.T) {
		t.Parallel()
		jigHome := t.TempDir()
		remote := newSourceAndRemote(t)
		pushCommit(t, remote, branch, "a1.txt", "first")
		lease, err := Acquire(jigHome, "fixture", remote, "main", branch, "T-1", Build, MustExistOnOrigin())
		if err != nil {
			t.Fatalf("first Acquire: %v", err)
		}
		run(t, remote, "branch", "-D", branch)

		_, err = Acquire(jigHome, "fixture", remote, "main", branch, "T-1", Build, MustExistOnOrigin())
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "BRANCH_NOT_FOUND" {
			t.Fatalf("Acquire after origin lost the branch: err = %v, want an *axi.Error BRANCH_NOT_FOUND", err)
		}
		if refExists(lease.Dir, "refs/remotes/origin/"+branch) {
			t.Fatal("the lease still holds a remote-tracking ref for a branch origin deleted: its view of origin is stale")
		}
	})

	t.Run("without the option a branch origin lacks is cut from the target", func(t *testing.T) {
		t.Parallel()
		remote := newSourceAndRemote(t)
		lease, err := Acquire(t.TempDir(), "fixture", remote, "main", "jig/T-1", "T-1", Build)
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		if head, main := run(t, lease.Dir, "rev-parse", "HEAD"), run(t, lease.Dir, "rev-parse", "origin/main"); head != main {
			t.Fatalf("lease HEAD = %s, want origin/main %s", head, main)
		}
	})
}

// TestAcquireRecutUnlessBuilt: a lease that has diverged from origin is re-cut
// from origin's tip, instead of refused, when it holds no commit jig built that
// origin lacks - the commits it has of its own are then the history an author's
// rewrite replaced, or an attempt's leftovers, and the branch is still
// origin's to follow. One that holds a commit jig built that origin lacks is
// refused as always, and a lease that has not diverged is not touched by the
// option: an ahead lease keeps its commits, and a behind one is fast-forwarded.
func TestAcquireRecutUnlessBuilt(t *testing.T) {
	t.Parallel()

	const branch = "add-retry"
	// setup leaves the lease diverged from origin: it holds `own`, origin
	// holds `theirs`, and they share the author's first commit.
	setup := func(t *testing.T) (jigHome, remote string, lease Lease, own, theirs string) {
		t.Helper()
		jigHome = t.TempDir()
		remote = newSourceAndRemote(t)
		pushCommit(t, remote, branch, "a1.txt", "first")
		lease, err := Acquire(jigHome, "fixture", remote, "main", branch, "T-1", Build)
		if err != nil {
			t.Fatalf("first Acquire: %v", err)
		}
		own = leaseCommit(t, lease.Dir, "own.txt", "in the lease")
		theirs = pushCommit(t, remote, branch, "a2.txt", "second")
		return jigHome, remote, lease, own, theirs
	}
	const unseen = "0123456789abcdef0123456789abcdef01234567"

	t.Run("a diverged lease holding none of them is re-cut", func(t *testing.T) {
		t.Parallel()
		for _, built := range [][]string{nil, {unseen}} {
			jigHome, remote, lease, own, theirs := setup(t)
			if _, err := Acquire(jigHome, "fixture", remote, "main", branch, "T-1", Build, RecutUnlessBuilt(built)); err != nil {
				t.Fatalf("Acquire over a diverged lease that holds none of %v: %v", built, err)
			}
			if head := run(t, lease.Dir, "rev-parse", "HEAD"); head != theirs {
				t.Fatalf("lease HEAD = %s, want origin's tip %s", head, theirs)
			}
			if got := run(t, lease.Dir, "symbolic-ref", "--short", "HEAD"); got != branch {
				t.Fatalf("lease is on %q, want %q checked out", got, branch)
			}
			if _, err := os.Stat(filepath.Join(lease.Dir, "own.txt")); !os.IsNotExist(err) {
				t.Fatalf("the lease still has the commit %s's file (stat err %v)", own, err)
			}
		}
	})

	t.Run("a diverged lease holding one is refused and left alone", func(t *testing.T) {
		t.Parallel()
		jigHome, remote, lease, own, theirs := setup(t)
		_, err := Acquire(jigHome, "fixture", remote, "main", branch, "T-1", Build, RecutUnlessBuilt([]string{unseen, own}))
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "BRANCH_DIVERGED" {
			t.Fatalf("Acquire over a lease holding a commit jig built: err = %v, want an *axi.Error BRANCH_DIVERGED", err)
		}
		if head := run(t, lease.Dir, "rev-parse", "HEAD"); head != own {
			t.Fatalf("lease HEAD = %s after the refusal, want jig's %s untouched", head, own)
		}
		if got := run(t, remote, "rev-parse", "refs/heads/"+branch); got != theirs {
			t.Fatalf("origin's %s = %s, want %s untouched", branch, got, theirs)
		}
	})

	// jig's commits were pushed, the lease then gained an attempt's leftover
	// on top of them, and the author pushed on: the lease has diverged, but
	// every commit jig built is on origin, and the leftover is all the lease
	// holds that origin lacks. Refusing would name work that is safe on origin,
	// and the help would merge the leftover into the branch.
	t.Run("a diverged lease whose built commits origin has too is re-cut", func(t *testing.T) {
		t.Parallel()
		jigHome := t.TempDir()
		remote := newSourceAndRemote(t)
		built := pushCommit(t, remote, branch, "a1.txt", "jig's, pushed")
		lease, err := Acquire(jigHome, "fixture", remote, "main", branch, "T-1", Build)
		if err != nil {
			t.Fatalf("first Acquire: %v", err)
		}
		leftover := leaseCommit(t, lease.Dir, "leftover.txt", "an attempt's, never verified")
		theirs := pushCommit(t, remote, branch, "a2.txt", "the author's")

		if _, err := Acquire(jigHome, "fixture", remote, "main", branch, "T-1", Build, RecutUnlessBuilt([]string{built})); err != nil {
			t.Fatalf("Acquire over a diverged lease whose only built commit is on origin: %v", err)
		}
		if head := run(t, lease.Dir, "rev-parse", "HEAD"); head != theirs {
			t.Fatalf("lease HEAD = %s, want origin's tip %s", head, theirs)
		}
		if _, err := os.Stat(filepath.Join(lease.Dir, "leftover.txt")); !os.IsNotExist(err) {
			t.Fatalf("the lease still has the leftover %s's file (stat err %v)", leftover, err)
		}
	})

	// The re-cut is git's own `checkout -B`, so like the fast-forward it stops
	// with git's error over an uncommitted edit it would overwrite, instead of
	// discarding the edit to make room: a lease can hold work in progress that
	// is not a commit at all.
	t.Run("a re-cut fails loudly over a local edit", func(t *testing.T) {
		t.Parallel()
		jigHome, remote, lease, own, theirs := setup(t)
		// The commit the re-cut discards is what the edit is to: own.txt
		// does not exist in origin's tip, so the re-cut would delete it.
		writeFile(t, filepath.Join(lease.Dir, "own.txt"), "edited in the lease")

		_, err := Acquire(jigHome, "fixture", remote, "main", branch, "T-1", Build, RecutUnlessBuilt(nil))
		if err == nil {
			t.Fatal("Acquire re-cut a lease over an uncommitted edit of a file the re-cut removes")
		}
		var ae *axi.Error
		if errors.As(err, &ae) {
			t.Fatalf("err = %v, want git's own failure, not an axi refusal", err)
		}
		if head := run(t, lease.Dir, "rev-parse", "HEAD"); head != own {
			t.Fatalf("lease HEAD = %s, want %s: the branch must not move", head, own)
		}
		if got, err := os.ReadFile(filepath.Join(lease.Dir, "own.txt")); err != nil || string(got) != "edited in the lease" {
			t.Fatalf("the lease's edit = %q (err %v), want it kept", got, err)
		}
		if got := run(t, remote, "rev-parse", "refs/heads/"+branch); got != theirs {
			t.Fatalf("origin's %s = %s, want %s untouched", branch, got, theirs)
		}
	})

	t.Run("without the option a diverged lease is refused", func(t *testing.T) {
		t.Parallel()
		jigHome, remote, _, _, _ := setup(t)
		_, err := Acquire(jigHome, "fixture", remote, "main", branch, "T-1", Build)
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "BRANCH_DIVERGED" {
			t.Fatalf("Acquire: err = %v, want an *axi.Error BRANCH_DIVERGED", err)
		}
	})

	t.Run("a lease that has not diverged is not touched", func(t *testing.T) {
		t.Parallel()
		jigHome := t.TempDir()
		remote := newSourceAndRemote(t)
		pushCommit(t, remote, branch, "a1.txt", "first")
		lease, err := Acquire(jigHome, "fixture", remote, "main", branch, "T-1", Build)
		if err != nil {
			t.Fatalf("first Acquire: %v", err)
		}
		own := leaseCommit(t, lease.Dir, "own.txt", "in the lease")
		if _, err := Acquire(jigHome, "fixture", remote, "main", branch, "T-1", Build, RecutUnlessBuilt(nil)); err != nil {
			t.Fatalf("Acquire over an ahead lease: %v", err)
		}
		if head := run(t, lease.Dir, "rev-parse", "HEAD"); head != own {
			t.Fatalf("lease HEAD = %s, want its own commit %s kept: it is ahead, not diverged", head, own)
		}
	})
}
