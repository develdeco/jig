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
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = s.Root
	return cmd.Run() == nil
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
	_, err = s.git("pull", "--rebase", "origin", branch)
	return err
}

// Push stages every change, commits it (skipping the commit when nothing is
// staged) and, when a remote exists, pushes it, retrying once with a
// pull --rebase on rejection.
func (s *Store) Push(msg string) error {
	if _, err := s.git("add", "-A"); err != nil {
		return err
	}
	staged, err := s.hasStagedChanges()
	if err != nil {
		return err
	}
	if staged {
		if _, err := s.git("-c", "user.name=jig", "-c", "user.email=jig@invalid", "commit", "-m", msg); err != nil {
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
	if _, err := s.git("push", "origin", branch); err != nil {
		if _, perr := s.git("pull", "--rebase", "origin", branch); perr != nil {
			return perr
		}
		if _, err2 := s.git("push", "origin", branch); err2 != nil {
			return err2
		}
	}
	return nil
}

func (s *Store) currentBranch() (string, error) {
	out, err := s.git("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (s *Store) hasStagedChanges() (bool, error) {
	cmd := exec.Command("git", "diff", "--cached", "--quiet")
	cmd.Dir = s.Root
	err := cmd.Run()
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return true, nil
	}
	return false, err
}

func (s *Store) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = s.Root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
