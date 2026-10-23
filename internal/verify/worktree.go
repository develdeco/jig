package verify

import (
	"errors"
	"fmt"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
)

// logPath is where the worker of a verification writes: the machine-local log
// that holds the oracle's output tail, which the journal line only
// summarizes.
func logPath(jigHome, storeID, ticket, sha, slice string) (string, error) {
	return home.VerifyLogPath(jigHome, storeID, ticket, sha, slice)
}

// LogPath is the log of the verification of sha (the full sha) for slice of
// ticket: <jig home>/logs/verify/<store id>/<ticket>/<sha12>-<slice>.log.
// The detached worker writes to it through its stdout; `jig verify --wait`
// opens it for append and hands it to Run as Deps.Log.
func LogPath(d Deps, ticket, sha, slice string) (string, error) {
	if d.Store == nil || d.JigHome == "" {
		return "", errors.New("verify: no store or jig home to place the log under")
	}
	id, err := d.Store.ID()
	if err != nil {
		return "", fmt.Errorf("verify: %w", err)
	}
	return logPath(d.JigHome, id, ticket, sha, slice)
}

// addWorktree makes the verification's worktree: a detached checkout of the
// commit from the operator's clone, at home.VerifyWorktreeDir. Whatever an
// earlier run of the same (commit, slice) left at that path - a worker that
// was killed before its cleanup - is removed first, which is safe because the
// caller holds the verification's lock.
func (x *run) addWorktree() error {
	if err := gitx.WorktreeRemove(x.d.Clone, x.dir); err != nil {
		return fmt.Errorf("clear the old worktree: %w", err)
	}
	if err := gitx.WorktreeAddDetached(x.d.Clone, x.dir, x.sha); err != nil {
		return fmt.Errorf("check the commit out: %w", err)
	}
	return nil
}

// removeWorktree takes the worktree away: git's removal, then the directory,
// then a prune of the clone's record of worktrees, in that order
// (gitx.WorktreeRemove). A failure is reported to the log, never to the
// verification: the outcome is already recorded, and the next run of the
// same (commit, slice) clears what is left.
func (x *run) removeWorktree() {
	if err := gitx.WorktreeRemove(x.d.Clone, x.dir); err != nil {
		x.logf("could not remove the worktree: %v", err)
	}
}
