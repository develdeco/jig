// Package pool manages a per-repo, per-key worktree pool under the jig home
// directory: full clones that are created once and reused across leases,
// with their working branch re-pointed to the right start point on each
// acquire.
package pool

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/develdeco/jig/gitx"
	"github.com/develdeco/jig/home"
)

// Lease is one checked-out worktree from the pool: a full clone of Repo,
// rooted at Dir, with Branch checked out.
type Lease struct {
	Dir    string
	Repo   string
	Key    string
	Branch string
}

// Return leaves the lease directory exactly as it is: the pool never
// deletes a worktree, so the next Acquire for the same key can resume it.
func (l Lease) Return() error {
	return nil
}

// Acquire returns the lease directory for repoName/key, cloning it from
// remote on first use or fetching on reuse, then re-pointing branch to the
// right start point:
//
//   - origin/<branch> if that ref exists (the branch has already been
//     pushed and jig should continue from the remote tip),
//   - else the local <branch> if it exists (a resumed lease: keep its
//     current commit as the start point, a no-op re-point),
//   - else origin/<target> (a fresh branch off the target).
func Acquire(repoName, remote, target, branch, key string) (Lease, error) {
	poolDir, err := home.PoolDir()
	if err != nil {
		return Lease{}, fmt.Errorf("pool: resolve pool dir: %w", err)
	}
	dir := filepath.Join(poolDir, repoName, key)

	if isGitRepo(dir) {
		if _, err := gitx.Run(dir, "fetch", "origin"); err != nil {
			return Lease{}, fmt.Errorf("pool: fetch %s: %w", dir, err)
		}
	} else {
		parent := filepath.Dir(dir)
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return Lease{}, fmt.Errorf("pool: create %s: %w", parent, err)
		}
		if _, err := gitx.Run(parent, "clone", remote, dir); err != nil {
			return Lease{}, fmt.Errorf("pool: clone %s: %w", remote, err)
		}
	}

	startPoint := "origin/" + target
	if refExists(dir, "refs/remotes/origin/"+branch) {
		startPoint = "origin/" + branch
	} else if refExists(dir, "refs/heads/"+branch) {
		startPoint = branch
	}
	if _, err := gitx.Run(dir, "checkout", "-B", branch, startPoint); err != nil {
		return Lease{}, fmt.Errorf("pool: checkout -B %s %s: %w", branch, startPoint, err)
	}

	return Lease{Dir: dir, Repo: repoName, Key: key, Branch: branch}, nil
}

// isGitRepo reports whether dir looks like an existing git working copy.
func isGitRepo(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && (fi.IsDir() || fi.Mode().IsRegular())
}

// refExists reports whether ref resolves to a commit in dir.
func refExists(dir, ref string) bool {
	_, err := gitx.Run(dir, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}
