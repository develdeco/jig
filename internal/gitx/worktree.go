package gitx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/filelock"
)

// worktreesLockName is the file in a repository's git common directory that
// jig's adds and removals of the repository's worktrees lock (filelock adds
// ".lock"): beside git's own records of the worktrees, and shared by every
// linked worktree of the clone and every process that works in it.
const worktreesLockName = "jig-worktrees"

// worktreesLockWait is how long an add or a removal waits for another
// process's to finish. A holder holds the lock only while git writes or
// deletes records, never for a checkout or a tree's deletion, and the lock is
// let go when its holder exits, so only a holder that is stuck makes the wait
// run out, even behind many others.
const worktreesLockWait = 2 * time.Minute

// errWorktreesBusy is a lock that another process held for the whole wait.
var errWorktreesBusy = errors.New("another process is adding or removing a worktree")

// lockWorktrees takes repoDir's repository's worktrees lock, waiting up to
// wait for it. git's worktree add, remove and prune read every worktree's
// records under the common directory's worktrees/ and add or delete records
// of their own without any lock, so two of them at once in one repository
// fail at random: an add dies reading the half-written records of another
// ("failed to read .../commondir"), and a prune deletes the records of an add
// that has not locked them yet. Holding this lock around each keeps jig's own
// processes from running into each other: the verify workers of one clone
// start together whenever several commits are verified at once.
func lockWorktrees(repoDir string, wait time.Duration) (release func(), err error) {
	common := filepath.Join(repoDir, ".git")
	if info, err := os.Stat(common); err != nil || !info.IsDir() {
		// A linked worktree's .git is a file, and a bare repository has none.
		if common, err = CommonDir(repoDir); err != nil {
			return nil, err
		}
	}
	release, held, err := filelock.TryLock(filepath.Join(common, worktreesLockName), wait)
	if err != nil {
		return nil, fmt.Errorf("gitx: lock the worktrees of %s: %w", repoDir, err)
	}
	if !held {
		return nil, fmt.Errorf("gitx: lock the worktrees of %s: %w (waited %s)", repoDir, errWorktreesBusy, wait)
	}
	return release, nil
}

// WorktreeAddDetached checks rev out, detached, into a new linked worktree of
// repoDir's repository at dir. The new worktree shares repoDir's object
// store and refs, so it costs a checkout and no fetch, and any commit repoDir
// has is there. dir must not exist, or be empty; git creates it.
//
// It holds the repository's worktrees lock (lockWorktrees) while git writes
// the worktree's records, and checks the commit out after letting go, as git
// itself does (a hard reset of the new worktree), so the checkouts of several
// adds run side by side. git's add with no checkout runs no post-checkout
// hook, so none of the repository's hooks runs in the checkout.
func WorktreeAddDetached(repoDir, dir, rev string) error {
	return worktreeAddDetached(repoDir, dir, rev, worktreesLockWait)
}

// worktreeAddDetached is WorktreeAddDetached with the lock's wait given, so a
// test can see the wait run out without waiting two minutes.
func worktreeAddDetached(repoDir, dir, rev string, wait time.Duration) error {
	if strings.HasPrefix(rev, "-") {
		return fmt.Errorf("gitx: worktree add: %q is not a revision", rev)
	}
	if err := addWorktreeRecords(repoDir, dir, rev, wait); err != nil {
		return err
	}
	// Once git's add returns, the records are whole, unlocked and name a dir
	// whose .git exists, so no other add or prune can trip on them.
	_, err := Run(dir, "reset", "--hard", "--no-recurse-submodules", "--quiet")
	return err
}

// addWorktreeRecords is the part of an add that holds the worktrees lock:
// git's add with no checkout, which writes the worktree's records, its .git
// file and its detached HEAD.
func addWorktreeRecords(repoDir, dir, rev string, wait time.Duration) error {
	release, err := lockWorktrees(repoDir, wait)
	if err != nil {
		return err
	}
	defer release()
	_, err = Run(repoDir, "worktree", "add", "--no-checkout", "--detach", dir, rev)
	return err
}

// WorktreeRemove takes the linked worktree at dir away from repoDir's
// repository, and the directory with it: it deletes the directory, then,
// holding the repository's worktrees lock (lockWorktrees), removes the
// worktree's record with git (forced twice, so a record git left locked goes
// too) and prunes the records of worktrees whose directory is gone. A dir
// that is not a worktree (the leftover of a run that died before git
// registered it) is deleted all the same, and a dir that is already gone is
// not an error: the call is safe to repeat, and to make before an add that
// may find the last run's remains. The error it returns is the directory
// still being there, the lock's wait running out, or git's prune failing.
func WorktreeRemove(repoDir, dir string) error {
	return worktreeRemove(repoDir, dir, worktreesLockWait)
}

// worktreeRemove is WorktreeRemove with the lock's wait given.
func worktreeRemove(repoDir, dir string, wait time.Duration) error {
	// The directory is the caller's alone, so it goes outside the lock, which
	// then covers only git's records; a removal of a large tree keeps no add
	// waiting.
	if err := removeAllWithRetry(dir); err != nil {
		return fmt.Errorf("gitx: remove worktree %s: %w", dir, err)
	}
	release, err := lockWorktrees(repoDir, wait)
	if err != nil {
		return err
	}
	defer release()
	// A failure is no news: the prune below clears the record of a worktree
	// whose directory is gone, and a dir git never registered is what git
	// calls "not a working tree".
	_, _ = Run(repoDir, "worktree", "remove", "--force", "--force", dir)
	if _, err := Run(repoDir, "worktree", "prune"); err != nil {
		return err
	}
	return nil
}

// removeAllWithRetry is os.RemoveAll that tries again for a short while: on
// Windows a directory a process has only just let go of (an oracle's child
// that exited a moment ago, the virus scanner reading what it wrote) refuses
// deletion for a few hundred milliseconds.
func removeAllWithRetry(dir string) error {
	tries := 1
	if runtime.GOOS == "windows" {
		tries = 10
	}
	var err error
	for i := 0; i < tries; i++ {
		if err = os.RemoveAll(dir); err == nil {
			return nil
		}
		time.Sleep(time.Duration(i+1) * 50 * time.Millisecond)
	}
	return err
}

// trailerKeyPattern is what a trailer key may be: a token of letters, digits
// and dashes, so it can be written into a --format without changing it.
var trailerKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

// Trailer returns the value of the commit trailer key (such as Jig-Slice) on
// commit sha in dir, as git interprets trailers: the last paragraph of the
// message, when it is all trailers. The key matches without regard to case.
// With several trailers of that key it returns the last; with none, "". It
// needs git 2.22 for the trailers format.
func Trailer(dir, sha, key string) (string, error) {
	if !trailerKeyPattern.MatchString(key) {
		return "", fmt.Errorf("gitx: trailer key %q must be letters, digits and dashes", key)
	}
	if strings.HasPrefix(sha, "-") {
		return "", fmt.Errorf("gitx: trailer: %q is not a revision", sha)
	}
	out, err := Run(dir, "show", "-s", "--format=%(trailers:key="+key+",valueonly,unfold)", sha)
	if err != nil {
		return "", err
	}
	value := ""
	for _, line := range strings.Split(out, "\n") {
		if v := strings.TrimSpace(line); v != "" {
			value = v
		}
	}
	return value, nil
}
