package gitx

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// WorktreeAddDetached checks rev out, detached, into a new linked worktree of
// repoDir's repository at dir. The new worktree shares repoDir's object
// store and refs, so it costs a checkout and no fetch, and any commit repoDir
// has is there. dir must not exist, or be empty; git creates it.
func WorktreeAddDetached(repoDir, dir, rev string) error {
	if strings.HasPrefix(rev, "-") {
		return fmt.Errorf("gitx: worktree add: %q is not a revision", rev)
	}
	_, err := Run(repoDir, "worktree", "add", "--detach", dir, rev)
	return err
}

// WorktreeRemove takes the linked worktree at dir away from repoDir's
// repository, and the directory with it: it removes the worktree with git
// (forced, so a tree the oracle left dirty goes too), deletes whatever is
// left of the directory, and prunes the repository's record of worktrees
// whose directory is gone. A dir that is not a worktree (the leftover of a
// run that died before git registered it) is deleted all the same, and a
// dir that is already gone is not an error: the call is safe to repeat, and
// to make before an add that may find the last run's remains. The error it
// returns is the directory still being there, or git's prune failing.
func WorktreeRemove(repoDir, dir string) error {
	// Forced twice: the second force also removes a worktree that is locked.
	// A failure is no news: the directory is dealt with below, whatever git
	// said, and an unregistered one is what git calls "not a working tree".
	_, _ = Run(repoDir, "worktree", "remove", "--force", "--force", dir)
	if err := removeAllWithRetry(dir); err != nil {
		return fmt.Errorf("gitx: remove worktree %s: %w", dir, err)
	}
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
