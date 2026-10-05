package store

import (
	"fmt"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
)

// maxClaimAttempts bounds how many times Claim re-mints after a push the
// origin rejects (see Claim), so two clones racing for an id can never chase
// a moving origin forever.
const maxClaimAttempts = 5

// idNotClaimedCode is Claim's refusal code once it gives up: neither a
// folder nor a commit survives it, so a re-run of the command that called
// Claim starts over from the store's current state, not from a half-landed
// claim.
const idNotClaimedCode = "ID_NOT_CLAIMED"

// Claim is the one way jig ticket new and jig graduate land a mint on the
// store's origin. write mints (or otherwise prepares) exactly one claim -
// almost always through Mint - and reports the id it claimed and every path
// it wrote, relative to the store root: Claim stages and commits exactly
// those paths with msg(id), leaving any other dirty state in the store
// untouched. On a store with no origin that commit is the whole claim, and
// write runs exactly once.
//
// On a store with an origin, Claim pushes the commit alone. A push the
// origin accepts finishes the claim. A push it rejects (the origin moved on:
// not a fast-forward) undoes the commit - `git reset --hard` to the commit
// before it, which restores an edited file (a chart's tickets.yaml) to what
// it held before, plus `git clean -fdx` on write's own paths, since a reset
// alone drops write's new ticket folder from git's tracked tree but leaves
// the now-empty directory - and its gitignored lock sidecar - sitting on
// disk (git never removes a directory merely for holding no tracked files) -
// pulls (always a fast-forward, since the undone commit was the only thing
// the local branch had that the origin lacked, so this can never end in a
// merge or rebase conflict) and calls write again, up to maxClaimAttempts
// times. A push that fails any other way
// (the origin unreachable, or anything else) undoes the claim the same way
// and refuses at once with ID_NOT_CLAIMED, since retrying blind would not
// help; so does the last of maxClaimAttempts rejections. Either way, nothing
// write wrote survives a failed Claim: no caller ever has to clean up a
// folder or a file edit Claim itself could not land.
func (s *Store) Claim(write func() (id string, paths []string, err error), msg func(id string) string) (string, error) {
	cp, err := s.begin()
	if err != nil {
		return "", err
	}
	hasRemote := cp.hasRemote()

	for attempt := 1; attempt <= maxClaimAttempts; attempt++ {
		pre, havePre := s.headSHA()

		id, paths, err := write()
		if err != nil {
			return "", err
		}
		if _, err := s.stageAndCommitPaths(paths, msg(id)); err != nil {
			if havePre {
				_ = s.undoClaim(pre, paths)
			}
			return "", err
		}
		if !hasRemote {
			return id, nil
		}

		rejected, perr := cp.pushOnce()
		if perr == nil {
			return id, nil
		}
		if havePre {
			if err := s.undoClaim(pre, paths); err != nil {
				return "", err
			}
		}
		if !rejected {
			return "", idNotClaimedError(perr)
		}
		if attempt == maxClaimAttempts {
			break
		}
		if err := cp.pull(); err != nil {
			return "", err
		}
	}
	return "", idNotClaimedError(fmt.Errorf("the origin rejected every push after %d attempts", maxClaimAttempts))
}

// headSHA returns the store's current HEAD commit, and whether it resolved:
// false only for an unborn branch (a brand-new store with nothing committed
// yet), which Claim can only see on a store with no origin, where nothing
// past this point ever needs to undo a commit.
func (s *Store) headSHA() (string, bool) {
	sha, err := gitx.Run(s.Root, "rev-parse", "--quiet", "--verify", "HEAD")
	if err != nil {
		return "", false
	}
	return sha, true
}

// undoClaim discards every commit after sha and restores the working tree to
// match it, then removes whatever of paths that leaves sitting on disk but
// untracked (a new ticket folder's now-empty directory) - how Claim undoes a
// push the origin rejected or otherwise refused. The clean also takes
// ignored files (-x): a ticket folder's only survivor past the reset is
// often store.Lock's own "*.lock" sidecar for its ticket.yaml, gitignored so
// it never shows up in `git status` - left in place, that sidecar alone
// would keep the folder on disk, and the next Mint's scan of the store root
// would then see it and skip past the very id this claim just gave up.
func (s *Store) undoClaim(sha string, paths []string) error {
	if _, err := gitx.Run(s.Root, "reset", "--hard", sha); err != nil {
		return err
	}
	args := append([]string{"clean", "-fdx", "--"}, paths...)
	_, err := gitx.Run(s.Root, args...)
	return err
}

// stageAndCommitPaths stages exactly paths (never -A) and, when anything is
// staged, commits them with jig's identity. Unlike stageAndCommit, it leaves
// any other dirty state in the store untouched, so a claim's commit holds
// only what write reported.
func (s *Store) stageAndCommitPaths(paths []string, msg string) (bool, error) {
	args := append([]string{"add", "--"}, paths...)
	if _, err := gitx.Run(s.Root, args...); err != nil {
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

// idNotClaimedError wraps cause as Claim's ID_NOT_CLAIMED refusal.
func idNotClaimedError(cause error) error {
	return &axi.Error{
		Msg:  fmt.Sprintf("the id could not be claimed on the store's origin: %v", cause),
		Code: idNotClaimedCode,
		Help: []string{"Retry once the origin is reachable"},
	}
}
