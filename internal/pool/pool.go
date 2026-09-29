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

	"github.com/develdeco/jig/internal/axi"
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
// repoName, <pool>/<repoName>/<ticket><suffix> in the pool under the jig
// home root jigHome, after checking that both name a single directory
// inside the pool.
func Dir(jigHome, repoName, ticket string, role Role) (string, error) {
	if repoName == "" || repoName == "." || repoName == ".." || strings.ContainsAny(repoName, `/\`) {
		return "", fmt.Errorf("pool: repo name %q must name a single directory", repoName)
	}
	if err := CheckTicket(ticket); err != nil {
		return "", fmt.Errorf("pool: %w", err)
	}
	if jigHome == "" {
		return "", fmt.Errorf("pool: no jig home given")
	}
	// Git runs the clone from the lease's parent directory, so a relative
	// jig home would otherwise be resolved twice.
	poolDir, err := filepath.Abs(home.PoolDir(jigHome))
	if err != nil {
		return "", fmt.Errorf("pool: resolve pool dir: %w", err)
	}
	return filepath.Join(poolDir, repoName, ticket+role.suffix()), nil
}

// Option adjusts one Acquire.
type Option func(*acquireOpts)

type acquireOpts struct {
	mustExistOnOrigin bool
	recutUnlessBuilt  bool
	built             []string
}

// MustExistOnOrigin makes Acquire refuse, with BRANCH_NOT_FOUND, when origin
// has no such branch, instead of cutting the branch from the target. The
// ordinary jig/<ticket> is such a branch until a publish pushes it, and is
// cut from the target on purpose; a branch a ticket recorded is on origin by
// definition, so one that is not there is gone, and cutting it from the
// target would build on something else.
func MustExistOnOrigin() Option {
	return func(o *acquireOpts) { o.mustExistOnOrigin = true }
}

// RecutUnlessBuilt makes Acquire follow origin over a lease of a branch that
// has diverged from it (see syncWithOrigin): when the lease's copy holds no
// commit of built - the commits jig built on the branch and verified - that
// origin lacks (HoldsUnpushedBuilt, the rule the gate applies to the same
// copy), it is re-cut from origin's tip instead of refused with
// BRANCH_DIVERGED. Its own commits are then the history an author's rewrite
// replaced, or an attempt's leftovers, or leftovers on top of jig's commits
// that origin has already, and the branch is still origin's to follow: the
// lease follows it. A copy that does hold one is refused as before, since
// re-cutting it would discard jig's work.
func RecutUnlessBuilt(built []string) Option {
	return func(o *acquireOpts) {
		o.recutUnlessBuilt = true
		o.built = built
	}
}

// Acquire returns ticket's role lease of repoName in the pool under the jig
// home root jigHome (see Dir), cloning it from remote on first use or
// fetching on reuse, then making sure branch is checked out:
//
//   - if the local <branch> already exists in this lease, it is checked out
//     as-is (a plain `checkout <branch>`, never `-B`): an existing local
//     branch is never reset, so commits an earlier slice in this same run
//     landed on it - pushed to origin or not - are never discarded. When
//     the branch also exists on origin, it is then synced with it (see
//     syncWithOrigin): fast-forwarded when it has no commits of its own,
//     kept when it is ahead of origin, and refused with BRANCH_DIVERGED
//     when each side has commits the other lacks (RecutUnlessBuilt: or
//     re-cut, when none of them is a commit jig built that origin lacks). A
//     branch that does not exist on origin is left exactly as it is;
//   - otherwise branch is created fresh with `checkout -B`, off
//     origin/<branch> if that ref exists (continue a branch pushed by an
//     earlier run) or else origin/<target>. MustExistOnOrigin refuses that
//     last fallback.
//
// A reused lease fetches with --prune, so its view of origin is origin's
// now: a branch deleted there is gone here too, and MustExistOnOrigin and
// the sync below never act on a stale ref.
//
// A lease is reused only when git opens it as its own repository. What git
// shows is not one (a .git that is not a directory, that resolves an
// enclosing repository, or that lacks HEAD, objects/ or refs/; leftover
// files; a file at the lease path) is moved aside, never deleted, and
// cloned afresh. A .git git refuses although it looks like a repository
// stops Acquire with git's error and is left as it is. See ownRepo and
// prepare.
func Acquire(jigHome, repoName, remote, target, branch, ticket string, role Role, opts ...Option) (Lease, error) {
	var o acquireOpts
	for _, opt := range opts {
		opt(&o)
	}
	dir, err := Dir(jigHome, repoName, ticket, role)
	if err != nil {
		return Lease{}, err
	}

	reuse, err := prepare(dir)
	if err != nil {
		return Lease{}, err
	}
	if reuse {
		if _, err := gitx.Run(dir, "fetch", "--prune", "origin"); err != nil {
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

	if o.mustExistOnOrigin && !refExists(dir, "refs/remotes/origin/"+branch) {
		return Lease{}, &axi.Error{
			Msg:  fmt.Sprintf("branch %q does not exist on origin", branch),
			Code: "BRANCH_NOT_FOUND",
			Help: []string{"Push the branch to origin, then rerun."},
		}
	}

	if refExists(dir, "refs/heads/"+branch) {
		// The local branch already exists: never reset it. A prior slice in
		// this run (or a resumed lease) may have committed on it, and that
		// work must survive this and every later Acquire of the same lease.
		if _, err := gitx.Run(dir, "checkout", branch); err != nil {
			return Lease{}, fmt.Errorf("pool: checkout %s: %w", branch, err)
		}
		if err := syncWithOrigin(dir, role, branch, o); err != nil {
			return Lease{}, err
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

// syncWithOrigin brings the branch checked out in dir in step with
// origin/<branch>, as of the fetch Acquire just made, when origin has that
// branch at all. A branch origin does not have is nobody else's to sync with
// and is left alone. Otherwise the lease's own copy is judged by Compare:
//
//   - InStep, or Ahead: nothing to do. An Ahead copy's extra commits are
//     jig's own, not yet pushed, and stay as they are;
//   - Behind: the lease has no commits of its own, so it is fast-forwarded to
//     origin's tip (`merge --ff-only`, which fails, as git says, over a dirty
//     file the new commits also change);
//   - Diverged: each side has commits the other lacks. jig merges, rebases
//     and resets neither, so the acquire is refused with BRANCH_DIVERGED and
//     the lease is left as it is - unless RecutUnlessBuilt says the lease
//     holds no commit jig built that origin lacks (HoldsUnpushedBuilt), and
//     the lease is re-cut from origin's tip (`checkout -B`, which like the
//     fast-forward stops with git's own error over an uncommitted edit it
//     would overwrite).
//
// The rule is the branch's, not the role's: a build, gate and publish lease
// each have a local copy that is compared with origin the same way.
func syncWithOrigin(dir string, role Role, branch string, o acquireOpts) error {
	remoteRef := "refs/remotes/origin/" + branch
	if !refExists(dir, remoteRef) {
		return nil
	}
	standing, err := Compare(dir, branch)
	if err != nil {
		return err
	}
	switch standing {
	case Behind:
		if _, err := gitx.Run(dir, "merge", "--ff-only", remoteRef); err != nil {
			return fmt.Errorf("pool: fast-forward %s to %s: %w", branch, remoteRef, err)
		}
	case Diverged:
		if o.recutUnlessBuilt {
			unpushed, err := HoldsUnpushedBuilt(dir, branch, o.built)
			if err != nil {
				return err
			}
			if !unpushed {
				if _, err := gitx.Run(dir, "checkout", "-B", branch, remoteRef); err != nil {
					return fmt.Errorf("pool: re-cut %s from %s: %w", branch, remoteRef, err)
				}
				return nil
			}
		}
		return DivergedError(dir, role, dir, branch)
	}
	return nil
}

// Standing is how a local copy of a branch stands against origin's.
type Standing int

const (
	// InStep: both are at the same commit.
	InStep Standing = iota
	// Behind: origin has commits the copy lacks, and the copy has none origin
	// lacks.
	Behind
	// Ahead: the copy has commits origin lacks, and origin has none the copy
	// lacks.
	Ahead
	// Diverged: each has commits the other lacks.
	Diverged
)

// Compare reports how the local branch in dir stands against
// refs/remotes/origin/<branch>, as of the last fetch. Both refs must exist.
func Compare(dir, branch string) (Standing, error) {
	localRef := "refs/heads/" + branch
	remoteRef := "refs/remotes/origin/" + branch
	local, err := gitx.RevParse(dir, localRef)
	if err != nil {
		return 0, fmt.Errorf("pool: resolve %s: %w", localRef, err)
	}
	remote, err := gitx.RevParse(dir, remoteRef)
	if err != nil {
		return 0, fmt.Errorf("pool: resolve %s: %w", remoteRef, err)
	}
	if local == remote {
		return InStep, nil
	}
	behind, err := gitx.IsAncestor(dir, local, remote)
	if err != nil {
		return 0, fmt.Errorf("pool: compare %s with %s: %w", localRef, remoteRef, err)
	}
	if behind {
		return Behind, nil
	}
	ahead, err := gitx.IsAncestor(dir, remote, local)
	if err != nil {
		return 0, fmt.Errorf("pool: compare %s with %s: %w", remoteRef, localRef, err)
	}
	if ahead {
		return Ahead, nil
	}
	return Diverged, nil
}

// DivergedError is the BRANCH_DIVERGED refusal for a copy of branch that
// stands Diverged from origin's. The copy is the one the role lease at
// leaseDir holds, and countDir is a repository that has it as
// refs/heads/<branch> and origin's as refs/remotes/origin/<branch>: the
// lease itself, or a clone of the copy. The refusal names neither side's
// author: a lease and its origin diverge when someone pushes while jig
// builds on the branch, when its history is rewritten, and when jig itself
// pushed a squash of the lease's commits. The lease's own view of origin can
// be older than the one compared here - a gate round compares in its own
// lease and never fetches into the build lease - so the help fetches before it
// merges.
func DivergedError(countDir string, role Role, leaseDir, branch string) error {
	localRef := "refs/heads/" + branch
	remoteRef := "refs/remotes/origin/" + branch
	ours, err := gitx.CommitsIn(countDir, remoteRef+".."+localRef)
	if err != nil {
		return fmt.Errorf("pool: list the commits of %s not on origin: %w", branch, err)
	}
	theirs, err := gitx.CommitsIn(countDir, localRef+".."+remoteRef)
	if err != nil {
		return fmt.Errorf("pool: list the commits of origin/%s not in the lease: %w", branch, err)
	}
	return &axi.Error{
		Msg: fmt.Sprintf("branch %s has diverged: the %s lease at %s has %d commit(s) origin/%s lacks, and origin/%s has %d the lease lacks",
			branch, role, leaseDir, len(ours), branch, branch, len(theirs)),
		Code: "BRANCH_DIVERGED",
		Help: []string{
			"jig merges, rebases and resets neither side: each has commits the other lacks",
			fmt.Sprintf("Integrate them in the %s lease (for example `git -C %s fetch origin && git -C %s merge origin/%s`), then rerun", role, leaseDir, leaseDir, branch),
		},
	}
}

// Usable reports whether dir is a lease the gate may reset in place: git
// opens it as its own repository (see ownRepo) and HEAD resolves to a
// commit. A reset anywhere else would act on an enclosing repository or
// fail on an unborn HEAD. probe trusts dir's owner, so a lease git refuses
// as another user's still passes; the gate's own reset then fails with
// that same ownership error instead of Acquire's fetch.
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
// prefix, a bare repository "false") and .git itself lacks HEAD, objects/ or
// refs/, which git requires of one; or git found no repository at all and
// .git lacks the same three. When dir's own .git has all three but git
// still would not use it - it fails outright, or (a corrupt HEAD, one a
// crash can leave truncated) it answers for an enclosing repository instead
// of dir - the lease may still hold unpushed work, so it returns git's
// error for a person to act on rather than trusting an answer that came
// from somewhere else.
func ownRepo(dir string) (bool, error) {
	gitDir := filepath.Join(dir, ".git")
	if fi, err := os.Lstat(gitDir); err != nil || !fi.IsDir() {
		return false, nil
	}
	// The prefix test compares no paths, so it holds whatever path spelling
	// git prints. Only an empty prefix confirms dir is itself the top level
	// git resolved; a non-empty prefix or "false" means git walked past
	// dir's own .git to answer for an enclosing repository, which must still
	// fall to the shape check below rather than being read as "not a
	// repository of its own".
	out, err := probe(dir, "rev-parse", "--is-inside-work-tree", "--show-prefix")
	if err == nil && out == "true" {
		return true, nil
	}
	if err == nil {
		err = fmt.Errorf("git resolved another repository from %s (rev-parse printed %q)", dir, out)
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
// holds survives for inspection; the pool never deletes it. dir is Stat'd,
// not Lstat'd, so a lease reached through a symlink or a Windows junction
// resolves to its target before ownRepo asks git about it, the way git
// itself would open it; only that resolved shape decides reuse or aside, so
// a link is renamed aside itself only once git also rejects its target.
func prepare(dir string) (bool, error) {
	fi, err := os.Stat(dir)
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
