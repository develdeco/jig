// Package pool manages a per-repo, per-key worktree pool under the jig home
// directory: full clones that are created once and reused across leases,
// with their working branch re-pointed to the right start point on each
// acquire.
package pool

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// Dir returns the absolute lease directory for ticket's role lease of
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
	// Git runs the clone from the lease's parent directory, so a relative
	// JIG_HOME would otherwise be resolved twice.
	poolDir, err = filepath.Abs(poolDir)
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
//
// A lease is reused only when git opens it as its own repository. What git
// shows is not one (a .git that is not a directory, that resolves an
// enclosing repository, or that lacks HEAD, objects/ or refs/; leftover
// files; a file at the lease path) is moved aside, never deleted, and
// cloned afresh. A .git git refuses although it looks like a repository
// stops Acquire with git's error and is left as it is. See ownRepo and
// prepare.
func Acquire(repoName, remote, target, branch, ticket string, role Role) (Lease, error) {
	dir, err := Dir(repoName, ticket, role)
	if err != nil {
		return Lease{}, err
	}

	reuse, err := prepare(dir)
	if err != nil {
		return Lease{}, err
	}
	if reuse {
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

// Usable reports whether dir is a lease the gate may reset in place: git
// opens it as its own repository (see ownRepo) and HEAD resolves to a
// commit. A reset anywhere else would act on an enclosing repository or
// fail on an unborn HEAD.
func Usable(dir string) bool {
	own, err := ownRepo(dir)
	if err != nil || !own {
		return false
	}
	_, err = probe(dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	return err == nil
}

// ownRepo reports whether git opens dir's own .git directory as the
// repository for dir, at its top level. It returns false with no error only
// when git shows dir is not a lease: .git is not a directory (a .git file
// could name any repository's git dir); git resolved an enclosing working
// copy or bare repository instead (a working copy prints a non-empty
// prefix, a bare repository "false"); or git found no repository and .git
// lacks HEAD, objects/ or refs/, which git requires of one. When git fails
// on a .git that has all three (an extension this git does not know, a
// corrupt config, git itself missing), the lease may still hold unpushed
// work, so it returns git's error for a person to act on.
func ownRepo(dir string) (bool, error) {
	gitDir := filepath.Join(dir, ".git")
	if fi, err := os.Lstat(gitDir); err != nil || !fi.IsDir() {
		return false, nil
	}
	// The prefix test compares no paths, so it holds whatever path spelling
	// git prints.
	out, err := probe(dir, "rev-parse", "--is-inside-work-tree", "--show-prefix")
	if err == nil {
		return out == "true", nil
	}
	for _, name := range []string{"HEAD", "objects", "refs"} {
		fi, serr := os.Lstat(filepath.Join(gitDir, name))
		if serr != nil || fi.IsDir() != (name != "HEAD") {
			return false, nil
		}
	}
	return false, fmt.Errorf("pool: git cannot open lease %s, whose .git looks like a repository; repair or remove it by hand: %w", dir, err)
}

// probe runs a read-only git query in dir that trusts dir's owner.
// Ownership is git's own check on every real command in the lease, so a
// lease git refuses as another user's (a JIG_HOME on exFAT or a network
// share, or one left behind by sudo) counts as a repository here and the
// fetch that follows fails with git's own explanation, instead of the lease
// being moved aside as broken.
func probe(dir string, args ...string) (string, error) {
	return gitx.Run(dir, append([]string{"-c", "safe.directory=*"}, args...)...)
}

// prepare readies dir for Acquire, reporting whether it holds a lease to
// reuse. A missing or empty directory is left for the clone. Anything else
// that git shows is not a repository of its own (see ownRepo) is moved
// aside to a timestamped sibling, <key>.broken-<UTC time>, so whatever it
// holds survives for inspection; the pool never deletes it.
func prepare(dir string) (bool, error) {
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("pool: inspect %s: %w", dir, err)
	}
	if fi.IsDir() {
		own, err := ownRepo(dir)
		if err != nil {
			return false, err
		}
		if own {
			return true, nil
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false, fmt.Errorf("pool: inspect %s: %w", dir, err)
		}
		if len(entries) == 0 {
			return false, nil
		}
	}

	aside := dir + ".broken-" + now().UTC().Format("20060102T150405Z")
	if _, err := os.Lstat(aside); err == nil {
		return false, fmt.Errorf("pool: lease %s is not a git repository of its own, and %s already exists; move or remove one of them by hand", dir, aside)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("pool: inspect %s: %w", aside, err)
	}
	if err := os.Rename(dir, aside); err != nil {
		return false, fmt.Errorf("pool: lease %s is not a git repository of its own and could not be moved aside (a process may still hold a file in it): %w", dir, err)
	}
	fmt.Fprintf(os.Stderr, "jig: lease %s was not a git repository of its own; moved it to %s and cloning afresh\n", dir, aside)
	return false, nil
}

// now is time.Now, swapped by tests that need a fixed aside name.
var now = time.Now

// refExists reports whether ref resolves to a commit in dir.
func refExists(dir, ref string) bool {
	_, err := gitx.Run(dir, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}
