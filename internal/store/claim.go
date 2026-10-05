package store

import (
	"fmt"
	"strings"

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
// not a fast-forward) undoes the commit - scoped to write's own paths alone
// (see undoClaim), never anything else in the store - restoring an edited
// file (a chart's tickets.yaml) to what it held before and removing write's
// new ticket folder, plus its gitignored lock sidecar, that a reset alone
// would otherwise leave sitting on disk (git never removes a directory
// merely for holding no tracked files) - pulls and calls write again, up to
// maxClaimAttempts times. That pull is a fast-forward in the common case
// (the undone commit was the only thing the local branch had that the
// origin lacked), but not always: a local change undoClaim's scoped undo
// preserved (another process's own edit or commit, still in the store) can
// overlap a path the rejecting push just brought in, and when it does the
// pull refuses rather than merging or rebasing; Claim reports that the same
// way as any other failed pull, ID_NOT_CLAIMED, naming the change. A push
// that fails any other way (the origin unreachable, or anything else) undoes
// the claim the same way and refuses at once with ID_NOT_CLAIMED, since
// retrying blind would not help; so does the last of maxClaimAttempts
// rejections. An undo that does not finish is reported the same way too,
// naming the commit it was undoing - the one case where a failed Claim can
// still leave something behind: undoClaim's own `reset --soft` has already
// moved the branch back before a later step of its own can fail, so that
// commit is off the branch either way, but a path it did not get to revert
// may still need finishing by hand, and there is nothing left for Claim
// itself to do but tell the operator. Otherwise nothing write wrote
// survives a failed Claim: no caller ever has to clean up a folder or a
// file edit Claim itself could not land.
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
			claimSHA, _ := s.headSHA()
			if err := s.undoClaim(pre, paths); err != nil {
				return "", undoFailedError(err, claimSHA)
			}
		}
		if !rejected {
			return "", idNotClaimedError(perr)
		}
		if attempt == maxClaimAttempts {
			break
		}
		if err := cp.pull(); err != nil {
			return "", pullAfterUndoError(s.Root, err)
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

// undoClaim discards exactly the commit write's own call just made - never
// anything else in the store - how Claim undoes a push the origin rejected
// or otherwise refused. `reset --soft` moves HEAD (and the branch) back to
// sha without touching the index or the working tree: an edit another
// process left uncommitted in the store (a journal.ndjson line) is
// untouched by it. A commit that process made of its own, on top of this
// claim's own commit (Store.Sync's or Store.Push's own `add -A`, racing in
// before this push), is orphaned rather than discarded - no longer
// reachable from the branch, its content left staged against the rewound
// HEAD; it is stageAndCommitPaths' own pathspec-scoped commit, not this
// reset, that keeps that content out of Claim's next retry (see
// stageAndCommitPaths). What paths held at sha is then restored path by
// path: `git restore`, staged and worktree alike, for a path that already
// existed there (an edited file such as a chart's tickets.yaml), or
// `git rm -r --cached` for one that did not (a new ticket folder, still
// staged as an addition after the soft reset) - keyed on
// gitx.PathExistsAtRev, which reports a tracked directory as existing too,
// unlike gitx.FileExistsAtRev's blob-only check, so a path that is itself a
// pre-existing folder is restored rather than wrongly taken for new and
// deleted. The clean that follows, scoped to paths alone, takes ignored
// files too (-x): a ticket folder's only survivor past that is often
// store.Lock's own "*.lock" sidecar for its ticket.yaml, gitignored so it
// never shows up in `git status` - left in place, that sidecar alone would
// keep the folder on disk, and the next Mint's scan of the store root would
// then see it and skip past the very id this claim just gave up.
func (s *Store) undoClaim(sha string, paths []string) error {
	if _, err := gitx.Run(s.Root, "reset", "--soft", sha); err != nil {
		return err
	}
	for _, p := range paths {
		existed, err := gitx.PathExistsAtRev(s.Root, sha, p)
		if err != nil {
			return err
		}
		if existed {
			if _, err := gitx.Run(s.Root, "restore", "--source="+sha, "--staged", "--worktree", "--", p); err != nil {
				return err
			}
		} else {
			if _, err := gitx.Run(s.Root, "rm", "-r", "--cached", "--", p); err != nil {
				return err
			}
		}
	}
	args := append([]string{"clean", "-fdx", "--"}, paths...)
	_, err := gitx.Run(s.Root, args...)
	return err
}

// stageAndCommitPaths stages exactly paths (never -A) and, when anything of
// theirs is staged, commits with jig's identity - the commit itself scoped
// to the same pathspec (`git commit -- <paths>`), not the whole index.
// Unlike stageAndCommit, it leaves any other dirty state in the store
// untouched, so a claim's commit holds only what write reported: without
// the pathspec, a commit here would instead sweep in whatever else the
// index happens to carry staged - undoClaim's `reset --soft` can leave
// another process's own commit staged exactly that way (see undoClaim) -
// under write's own message, losing that other commit's identity and
// pushing its content as if it were this claim's.
func (s *Store) stageAndCommitPaths(paths []string, msg string) (bool, error) {
	args := append([]string{"add", "--"}, paths...)
	if _, err := gitx.Run(s.Root, args...); err != nil {
		return false, err
	}
	staged, err := s.hasStagedChanges(paths...)
	if err != nil {
		return false, err
	}
	if !staged {
		return false, nil
	}
	commitArgs := append([]string{"-c", "user.name=jig", "-c", "user.email=jig@invalid", "commit", "-m", msg, "--"}, paths...)
	if _, err := gitx.Run(s.Root, commitArgs...); err != nil {
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

// undoFailedError wraps a failed undoClaim as ID_NOT_CLAIMED too, naming
// sha, the claim's own rejected commit. Unlike every other refusal Claim
// gives, where nothing write wrote survives, this one can still leave
// something behind: undoClaim's own `reset --soft`, its first step, has
// already moved the branch back before any later, per-path step can fail,
// so sha is no longer reachable from the branch - but it still exists in
// the store (not garbage-collected away), and the working tree may carry a
// path undoClaim never finished reverting. Naming sha gives the operator
// something to inspect rather than a bare git error.
func undoFailedError(cause error, sha string) error {
	if sha == "" {
		sha = "unknown"
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("the id could not be claimed on the store's origin, and reverting its rejected commit %s did not finish: %v", sha, cause),
		Code: idNotClaimedCode,
		Help: []string{fmt.Sprintf("Commit %s is off the branch but still in the store: inspect it with `git show %s`, check `git status` for a path left partially reverted, finish by hand, then retry.", sha, sha)},
	}
}

// pullAfterUndoError wraps a failed pull as ID_NOT_CLAIMED, naming the local
// change still in the store (root) that blocked it - an edit or commit
// undoClaim's own scoped undo preserved (see undoClaim), overlapping a path
// the rejecting push just brought in. Best-effort: when root's own dirty
// paths cannot be read either, the message falls back to cause alone rather
// than failing to report the original failure.
func pullAfterUndoError(root string, cause error) error {
	paths, _ := dirtyPaths(root)
	where := "an unrecorded local change"
	if len(paths) > 0 {
		where = strings.Join(paths, ", ")
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("the id could not be claimed: pulling in the commit that rejected it failed, blocked by %s: %v", where, cause),
		Code: idNotClaimedCode,
		Help: []string{fmt.Sprintf("The store still holds a local change in %s that the pull would overwrite: commit or stash it by hand there, then retry.", where)},
	}
}
