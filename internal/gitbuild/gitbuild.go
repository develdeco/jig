// Package gitbuild holds the builder moves a test makes to set a repository
// up: commit everything, push a branch, fetch one. It is test support, a leaf
// over gitx that any package's tests import.
//
// The code under test runs real git on real repositories (ADR 0011). Only a
// test's own setup moves leave the git program, and only through gitx: each
// move is made in process where gitx accepts the repository and falls back to
// the git program where it declines, so a test sees the same result either
// way and a repository gitx cannot serve (a hook, a non-bare remote, the first
// push of a branch) still gets its move. Moves gitx does not do (apply,
// checkout, merge, switch, reset, branch) stay gitx.RunEnv at the call site.
package gitbuild

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// Builder makes a test's builder moves under one environment.
type Builder struct {
	t   testing.TB
	env []string

	// Fallback runs the git program for a move gitx declines (ErrUseCLI);
	// gitx.RunEnv unless a test replaces it, to count the declines.
	Fallback func(dir string, env []string, args ...string) (string, error)

	// ChildEnv is the whole environment Fallback runs the git program in,
	// when it is not the process's with env laid over it (the default).
	ChildEnv func() []string
}

// New returns a Builder for t whose commits carry the identity and dates in
// env (KEY=VALUE entries, as gitx.RunEnv takes them), which also reach every
// git program the Builder falls back to. It never edits the process
// environment.
func New(t testing.TB, env []string) *Builder {
	t.Helper()
	return &Builder{t: t, env: env, Fallback: gitx.RunEnv}
}

// Commit stages everything in dir and commits it with msg, as `git add -A`
// and `git commit` do. Nothing to commit fails the test, as the git program
// does.
func (b *Builder) Commit(dir, msg string) {
	b.t.Helper()
	if b.hooked(dir) {
		b.run(dir, "add", "-A")
		b.run(dir, "commit", "-q", "-m", msg)
		return
	}
	if repo, err := gitx.OpenRepo(dir); err == nil {
		committed, err := repo.CommitAllEnv(msg, b.env)
		if err == nil {
			if !committed {
				b.t.Fatalf("gitbuild: nothing to commit in %s", dir)
			}
			return
		}
		if !errors.Is(err, gitx.ErrUseCLI) {
			b.t.Fatalf("gitbuild: commit in %s: %v", dir, err)
		}
	} else if !errors.Is(err, gitx.ErrUseCLI) {
		b.t.Fatalf("gitbuild: open %s: %v", dir, err)
	}
	b.run(dir, "add", "-A")
	b.run(dir, "commit", "-q", "-m", msg)
}

// Push pushes branch (or a refspec, which is the git program's) of the repository in dir to remote. A push the remote
// rejects as not a fast-forward fails the test, as the git program's push
// does.
func (b *Builder) Push(dir, remote, branch string) {
	b.t.Helper()
	if b.hooked(dir) {
		b.run(dir, "push", "-q", remote, branch)
		return
	}
	// A refspec (HEAD~1:refs/heads/x) is no branch name gitx knows.
	if strings.ContainsAny(branch, ":~^") {
		b.run(dir, "push", "-q", remote, branch)
		return
	}
	if repo, err := gitx.OpenRepo(dir); err == nil {
		err := repo.Push(remote, branch)
		if err == nil {
			return
		}
		if errors.Is(err, gitx.ErrPushRejected) {
			b.t.Fatalf("gitbuild: push %s to %s from %s rejected: %v", branch, remote, dir, err)
		}
		if !errors.Is(err, gitx.ErrUseCLI) {
			b.t.Fatalf("gitbuild: push %s from %s: %v", branch, dir, err)
		}
	} else if !errors.Is(err, gitx.ErrUseCLI) {
		b.t.Fatalf("gitbuild: open %s: %v", dir, err)
	}
	b.run(dir, "push", "-q", remote, branch)
}

// Fetch brings branch from remote into the repository in dir's
// remote-tracking branch, changing neither its branch nor its work tree.
func (b *Builder) Fetch(dir, remote, branch string) {
	b.t.Helper()
	if repo, err := gitx.OpenRepo(dir); err == nil {
		rel, err := repo.Fetch(remote, branch)
		if err == nil && rel != gitx.Diverged {
			return
		}
		// A diverged branch is left to the git program, which fetches it.
		if err != nil && !errors.Is(err, gitx.ErrUseCLI) {
			b.t.Fatalf("gitbuild: fetch %s from %s: %v", branch, dir, err)
		}
	} else if !errors.Is(err, gitx.ErrUseCLI) {
		b.t.Fatalf("gitbuild: open %s: %v", dir, err)
	}
	b.run(dir, "fetch", "-q", remote, branch)
}

// hooked reports whether the git program would run a hook the repository's own
// hooks directory does not show (core.hooksPath in the repository's or the
// global config the child sees), which gitx cannot run: the move is then the
// git program's, as it is for a repository with hooks of its own, since a hook
// may be the very behavior under test (a global hook that starts a
// verification at each commit).
func (b *Builder) hooked(dir string) bool {
	var env []string
	if b.ChildEnv != nil {
		env = b.ChildEnv()
	} else {
		env = append(os.Environ(), b.env...)
	}
	return gitx.HooksConfigured(dir, env)
}

func (b *Builder) run(dir string, args ...string) {
	b.t.Helper()
	if _, err := b.Fallback(dir, b.env, args...); err != nil {
		b.t.Fatalf("gitbuild: git %v in %s: %v", args, dir, err)
	}
}
