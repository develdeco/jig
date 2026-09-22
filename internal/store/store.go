// Package store implements the jig truth-repo store: the git-backed tree of
// ticket folders, slice state, questions and the locked/atomic file
// primitives every writer in jig builds on.
package store

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
)

// Store is a truth-repo checkout rooted at Root.
type Store struct {
	Root string
}

// Open opens the store rooted at root. root must contain a project.yaml.
func Open(root string) (*Store, error) {
	if _, err := os.Stat(filepath.Join(root, "project.yaml")); err != nil {
		return nil, fmt.Errorf("store: open %s: %w", root, err)
	}
	return &Store{Root: root}, nil
}

// TicketDir returns the ticket folder path for id, rooted at Root.
func (s *Store) TicketDir(id string) string {
	return filepath.Join(s.Root, id)
}

// HasRemote reports whether the store has a git remote named "origin".
func (s *Store) HasRemote() bool {
	_, err := gitx.Run(s.Root, "remote", "get-url", "origin")
	return err == nil
}

// Sync stages and commits any uncommitted store state left behind by an
// earlier command that failed after writing to the store - for example a
// gate that appended its gate-open journal line and then failed at an
// oracle, or failed on SLICE_ID_DUPLICATE after writing its round - then
// pulls with rebase when a remote exists. Without this, a dirty tree makes
// every later command's Sync fail with "cannot pull with rebase", wedging
// the store until someone commits by hand. Committing the leftovers does
// not retry the failed command's round: Sync records them as a jig commit
// and the command proceeds, but a partial gate round directory still counts
// as a round, so the next `jig gate` opens round N+1 rather than replaying
// the failed one. It is a silent no-op when there is no remote. Callers run
// it at the start of every command.
func (s *Store) Sync() error {
	if !s.HasRemote() {
		return nil
	}
	if _, err := s.stageAndCommit("jig: record uncommitted store state"); err != nil {
		return err
	}
	branch, err := s.currentBranch()
	if err != nil {
		return err
	}
	if _, err := gitx.Run(s.Root, "pull", "--rebase", "origin", branch); err != nil {
		return s.abortFailedPull(err, branch)
	}
	return nil
}

// refuseIfMidRebaseOrMerge errors when the store has an unfinished rebase or
// merge in progress, or unresolved conflict markers already sitting in the
// index, without touching the index itself. Neither Sync nor Push must ever
// stage or commit over that: `git add -A` would pick up unresolved conflict
// markers, and a later `rebase --continue` (or manual resolution) would
// then commit them onto the store branch, corrupting whatever file
// conflicted (e.g. journal.ndjson) for every later reader. It is called from
// stageAndCommit, the shared first step of both Sync and Push, so it guards
// a command that only ever Pushes (e.g. `jig requeue`) too - not only the
// commands that Sync first.
func (s *Store) refuseIfMidRebaseOrMerge() error {
	mid, err := inProgressRebaseOrMerge(s.Root)
	if err != nil {
		return err
	}
	if !mid {
		return nil
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("the store at %s has an unfinished rebase or merge, or unresolved conflicts", s.Root),
		Code: "STORE_CONFLICT",
		Help: []string{"Check the store's state there with `git status`, resolve it, then rerun."},
	}
}

// inProgressRebaseOrMerge reports whether dir has an unfinished rebase (git
// leaves a rebase-merge or rebase-apply directory under .git for the
// duration of one), an unresolved merge (MERGE_HEAD), an unfinished
// cherry-pick or revert (CHERRY_PICK_HEAD or REVERT_HEAD - git keeps these
// set even once the conflict is resolved and staged, until `--continue` or
// `--abort` runs), a multi-commit cherry-pick or revert sequence (the
// sequencer directory), an unfinished bisect (BISECT_LOG), or unmerged
// index entries left by something other than any of those - a conflicted
// `git stash pop` leaves the index with conflict markers on disk but none
// of these markers. The git-path markers are read with one `rev-parse` call
// rather than one per marker, since this runs on every Sync and every Push.
// The unmerged-index check uses `git ls-files -u`, which only reads the
// index, rather than `git diff --diff-filter=U`, which opportunistically
// rewrites .git/index as a side effect - a write this read-only guard,
// called on every Sync and Push, must not make.
func inProgressRebaseOrMerge(dir string) (bool, error) {
	out, err := gitx.Run(dir, "rev-parse",
		"--git-path", "rebase-merge",
		"--git-path", "rebase-apply",
		"--git-path", "MERGE_HEAD",
		"--git-path", "CHERRY_PICK_HEAD",
		"--git-path", "REVERT_HEAD",
		"--git-path", "sequencer",
		"--git-path", "BISECT_LOG",
	)
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(out, "\n") {
		p := strings.TrimSpace(line)
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if _, err := os.Stat(p); err == nil {
			return true, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	unmerged, err := gitx.Run(dir, "ls-files", "-u")
	if err != nil {
		return false, err
	}
	return unmerged != "", nil
}

// stageAndCommit refuses while the store has an unfinished rebase or merge,
// then stages every change (`add -A`) and, when anything is staged, commits
// it with jig's identity and msg. It reports whether a commit was made.
func (s *Store) stageAndCommit(msg string) (bool, error) {
	if err := s.refuseIfMidRebaseOrMerge(); err != nil {
		return false, err
	}
	if _, err := gitx.Run(s.Root, "add", "-A"); err != nil {
		return false, err
	}
	staged, err := s.hasStagedChanges()
	if err != nil {
		return false, err
	}
	if !staged {
		return false, nil
	}
	if _, err := gitx.Run(s.Root, "-c", "user.name=jig", "-c", "user.email=jig@invalid", "commit", "-m", msg); err != nil {
		return false, err
	}
	return true, nil
}

// Push stages every change, commits it (skipping the commit when nothing is
// staged) and, when a remote exists, pushes it, retrying once with a
// pull --rebase on rejection.
func (s *Store) Push(msg string) error {
	if _, err := s.stageAndCommit(msg); err != nil {
		return err
	}
	if !s.HasRemote() {
		return nil
	}
	branch, err := s.currentBranch()
	if err != nil {
		return err
	}
	if _, err := gitx.Run(s.Root, "push", "origin", branch); err != nil {
		if _, perr := gitx.Run(s.Root, "pull", "--rebase", "origin", branch); perr != nil {
			return s.abortFailedPull(perr, branch)
		}
		if _, err2 := gitx.Run(s.Root, "push", "origin", branch); err2 != nil {
			return err2
		}
	}
	// Keep the long-lived store packed. Best-effort: a maintenance failure
	// (for example a lock held by the user's own maintenance) must not fail
	// a push that already succeeded.
	_ = gitx.MaintenanceAuto(s.Root)
	return nil
}

// abortFailedPull handles jig's own failed `pull --rebase` on branch. A
// pull that stopped on a conflict leaves a rebase in progress
// (stageAndCommit refused any rebase or merge that was already there, so
// this one is jig's own): the conflicting paths are read structurally
// (`git ls-files -u`, before anything else touches the index) so the report
// can name them, then the rebase is aborted with a best-effort `rebase
// --abort`, and the result reported as STORE_CONFLICT either way, since the
// store is still mid-rebase if the abort itself failed (for example a
// Windows file lock) and needs the same manual resolution. A pull that
// failed before rebasing (an unreachable or moved remote, an auth failure)
// left nothing to abort, so its error is returned unchanged rather than
// misreported as a conflict. When the state cannot be read, the abort is
// still attempted (best effort), and the read's own error is carried into
// the wrapped message rather than assumed away.
func (s *Store) abortFailedPull(pullErr error, branch string) error {
	mid, stateErr := inProgressRebaseOrMerge(s.Root)
	if stateErr == nil && !mid {
		return pullErr
	}
	paths, _ := conflictedPaths(s.Root)
	_, abortErr := gitx.Run(s.Root, "rebase", "--abort")
	var sha string
	if abortErr == nil {
		sha, _ = gitx.Run(s.Root, "rev-parse", "HEAD")
	}
	return s.wrapAbortedPullConflict(abortErr, stateErr, paths, sha, branch)
}

// conflictedPaths returns the unique paths with unmerged (conflicted) index
// entries in dir, in the order `git ls-files -u` lists them. Each conflicted
// path appears once per stage (1/2/3) in that output; this collapses them to
// one entry per path. Like inProgressRebaseOrMerge's own check, it reads
// only the index and never rewrites it.
func conflictedPaths(dir string) ([]string, error) {
	out, err := gitx.Run(dir, "ls-files", "-u")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		i := strings.IndexByte(line, '\t')
		if i < 0 {
			continue
		}
		p := line[i+1:]
		if seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}
	return paths, nil
}

// wrapAbortedPullConflict turns a failed pull --rebase into a STORE_CONFLICT
// the operator can act on, after jig's own best-effort `rebase --abort` has
// run (abortErr is that attempt's result, nil on success) and after the
// state read that decided whether to attempt it (stateErr, non-nil when
// inProgressRebaseOrMerge itself could not read the store's state). The
// message is built entirely from state jig itself read - the conflicting
// paths (read before the abort) and, once aborted, the commit the store
// landed back on - never from git's stderr: that text carries `rebase
// --continue`/`--skip`/`--abort` hints for the rebase jig has just aborted,
// which fail if followed as printed. When the abort itself failed, the
// message says so plainly instead of falsely claiming the rebase was
// aborted. If the state read had also failed, the message does not assert
// the store is mid-rebase either - that was never confirmed - and instead
// says the store's state is unknown, pointing at `git status` in the store
// rather than at a specific rebase to abort or continue.
func (s *Store) wrapAbortedPullConflict(abortErr, stateErr error, paths []string, sha, branch string) error {
	if abortErr != nil {
		if stateErr != nil {
			return &axi.Error{
				Msg:  fmt.Sprintf("the store at %s: pull --rebase of origin/%s failed, its state could not be read (%v), and the best-effort `rebase --abort` also failed: %v", s.Root, branch, stateErr, abortErr),
				Code: "STORE_CONFLICT",
				Help: []string{fmt.Sprintf("The store at %s is in an unknown state: check it there with `git status`, then resolve whatever it shows.", s.Root)},
			}
		}
		return &axi.Error{
			Msg:  fmt.Sprintf("the store at %s: pull --rebase of origin/%s conflicted, and the best-effort `rebase --abort` also failed: %v", s.Root, branch, abortErr),
			Code: "STORE_CONFLICT",
			Help: []string{fmt.Sprintf("The store at %s is still mid-rebase: resolve it there with `git status`, then `git rebase --abort` or `--continue`.", s.Root)},
		}
	}
	where := "an unrecorded path"
	if len(paths) > 0 {
		where = strings.Join(paths, ", ")
	}
	at := sha
	if at == "" {
		at = "unknown"
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("the store at %s: pull --rebase of origin/%s conflicted in %s and was aborted by jig; the store is back at its pre-pull commit %s", s.Root, branch, where, at),
		Code: "STORE_CONFLICT",
		Help: []string{fmt.Sprintf("Resolve the divergence in the store: `git pull --rebase origin %s` there, fix the conflict in %s, then rerun.", branch, where)},
	}
}

// currentBranch returns the checked-out branch name.
func (s *Store) currentBranch() (string, error) {
	return gitx.Run(s.Root, "rev-parse", "--abbrev-ref", "HEAD")
}

// hasStagedChanges reports whether the index differs from HEAD: "diff
// --cached --quiet" exits 1 for staged changes; any other failure is an
// error.
func (s *Store) hasStagedChanges() (bool, error) {
	_, err := gitx.Run(s.Root, "diff", "--cached", "--quiet")
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, err
}
