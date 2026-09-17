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

// Sync pulls with rebase when a remote exists; it is a silent no-op
// otherwise. Callers run it at the start of every command.
func (s *Store) Sync() error {
	if !s.HasRemote() {
		return nil
	}
	branch, err := s.currentBranch()
	if err != nil {
		return err
	}
	_, err = gitx.Run(s.Root, "pull", "--rebase", "origin", branch)
	return err
}

// Push stages every change, commits it (skipping the commit when nothing is
// staged) and, when a remote exists, pushes it, retrying once with a
// pull --rebase on rejection.
func (s *Store) Push(msg string) error {
	if _, err := gitx.Run(s.Root, "add", "-A"); err != nil {
		return err
	}
	staged, err := s.hasStagedChanges()
	if err != nil {
		return err
	}
	if staged {
		if _, err := gitx.Run(s.Root, "-c", "user.name=jig", "-c", "user.email=jig@invalid", "commit", "-m", msg); err != nil {
			return err
		}
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
			return perr
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
