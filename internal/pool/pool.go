// Package pool manages a per-repo, per-key worktree pool under the jig home
// directory: full clones that are created once and reused across leases,
// with their working branch re-pointed to the right start point on each
// acquire.
package pool

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
)

// Role is which of a ticket's leases a caller wants. Each role is its own
// directory, pool/<repo>/<ticket><suffix>, so a ticket's build, gate and
// publish work never share a working copy.
type Role int

const (
	// Build is the lease a ticket's slices commit on: pool/<repo>/<ticket>.
	Build Role = iota
	// Gate is the lease the gate verifies in: pool/<repo>/<ticket>-gate.
	Gate
	// Publish is the lease publish squashes in: pool/<repo>/<ticket>-publish.
	Publish
)

// suffix is the role's key suffix after the ticket id; Build has none.
func (r Role) suffix() string {
	switch r {
	case Gate:
		return "-gate"
	case Publish:
		return "-publish"
	}
	return ""
}

// String names the role in messages.
func (r Role) String() string {
	switch r {
	case Gate:
		return "gate"
	case Publish:
		return "publish"
	}
	return "build"
}

// Lease is one checked-out worktree from the pool: a full clone of Repo,
// rooted at Dir, with Branch checked out.
type Lease struct {
	Dir    string
	Repo   string
	Branch string
}

// Return leaves the lease directory exactly as it is: the pool never
// deletes a worktree, so the next Acquire for the same ticket and role can
// resume it.
func (l Lease) Return() error {
	return nil
}

// CheckTicket reports why ticket cannot name a ticket's leases, or nil when
// it can. A lease key is the ticket id plus its role's suffix, used as one
// directory name, so the id must be a single path component that does not
// start with a dot (which also rules out "." and ".."), and must not end in
// a role suffix: ticket X-gate's build lease would be ticket X's gate lease,
// which the gate resets and cleans. The suffix check ignores case and
// trailing dots and spaces, since a case-insensitive filesystem (the Windows
// and macOS default) resolves X-GATE to X-gate's directory, and Windows also
// drops a name's trailing dots and spaces.
func CheckTicket(ticket string) error {
	switch {
	case ticket == "":
		return errors.New("ticket id is empty")
	case strings.ContainsAny(ticket, `/\`):
		return fmt.Errorf("ticket id %q contains a path separator; it must name a single directory", ticket)
	case strings.HasPrefix(ticket, "."):
		return fmt.Errorf("ticket id %q starts with a dot; it must name a plain, visible directory", ticket)
	}
	base := strings.ToLower(strings.TrimRight(ticket, ". "))
	for _, r := range []Role{Gate, Publish} {
		if strings.HasSuffix(base, r.suffix()) {
			return fmt.Errorf("ticket id %q ends in %q, which jig reserves for a ticket's %s lease", ticket, r.suffix(), r)
		}
	}
	return nil
}

// Dir returns the lease directory for ticket's role lease of
// repoName, <pool>/<repoName>/<ticket><suffix>, after checking that both
// name a single directory inside the pool.
func Dir(repoName, ticket string, role Role) (string, error) {
	if repoName == "" || repoName == "." || repoName == ".." || strings.ContainsAny(repoName, `/\`) {
		return "", fmt.Errorf("pool: repo name %q must name a single directory", repoName)
	}
	if err := CheckTicket(ticket); err != nil {
		return "", fmt.Errorf("pool: %w", err)
	}
	poolDir, err := home.PoolDir()
	if err != nil {
		return "", fmt.Errorf("pool: resolve pool dir: %w", err)
	}
	return filepath.Join(poolDir, repoName, ticket+role.suffix()), nil
}

// Acquire returns ticket's role lease of repoName, cloning it from remote on
// first use or fetching on reuse, then making sure branch is checked out:
//
//   - if the local <branch> already exists in this lease, it is checked out
//     as-is (a plain `checkout <branch>`, never `-B`): an existing local
//     branch is never reset, so commits an earlier slice in this same run
//     landed on it - pushed to origin or not - are never discarded;
//   - otherwise branch is created fresh with `checkout -B`, off
//     origin/<branch> if that ref exists (continue a branch pushed by an
//     earlier run) or else origin/<target>.
func Acquire(repoName, remote, target, branch, ticket string, role Role) (Lease, error) {
	dir, err := Dir(repoName, ticket, role)
	if err != nil {
		return Lease{}, err
	}

	if isGitRepo(dir) {
		if _, err := gitx.Run(dir, "fetch", "origin"); err != nil {
			return Lease{}, fmt.Errorf("pool: fetch %s: %w", dir, err)
		}
		// Keep the reused clone packed. Best-effort: a maintenance failure
		// must not fail an Acquire whose fetch already succeeded.
		_ = gitx.MaintenanceAuto(dir)
	} else {
		parent := filepath.Dir(dir)
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return Lease{}, fmt.Errorf("pool: create %s: %w", parent, err)
		}
		if _, err := gitx.Run(parent, "clone", remote, dir); err != nil {
			return Lease{}, fmt.Errorf("pool: clone %s: %w", remote, err)
		}
	}

	if refExists(dir, "refs/heads/"+branch) {
		// The local branch already exists: never reset it. A prior slice in
		// this run (or a resumed lease) may have committed on it, and that
		// work must survive this and every later Acquire of the same lease.
		if _, err := gitx.Run(dir, "checkout", branch); err != nil {
			return Lease{}, fmt.Errorf("pool: checkout %s: %w", branch, err)
		}
	} else {
		startPoint := "origin/" + target
		if refExists(dir, "refs/remotes/origin/"+branch) {
			startPoint = "origin/" + branch
		}
		if _, err := gitx.Run(dir, "checkout", "-B", branch, startPoint); err != nil {
			return Lease{}, fmt.Errorf("pool: checkout -B %s %s: %w", branch, startPoint, err)
		}
	}

	return Lease{Dir: dir, Repo: repoName, Branch: branch}, nil
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
