package store

import (
	"errors"
	"fmt"
	"regexp"
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
// pull refuses rather than merging or rebasing. Claim reports that as
// ID_NOT_CLAIMED too, naming the local change only when the pull's own
// failure actually names one - git's "would be overwritten by merge" text,
// or a rebase conflict's own STORE_CONFLICT (abortFailedPull), which Claim
// returns unchanged, Code and Help both, since that already carries the
// real recipe; anything else - an unreachable or moved origin, a dead
// network - is reported as what it is, not blamed on a file that was never
// in the way. A push that fails any other way (the origin unreachable, or
// anything else) undoes the claim the same way and refuses at once with
// ID_NOT_CLAIMED, since retrying blind would not help; so does the last of
// maxClaimAttempts rejections. An undo that does not finish is reported the
// same way too, naming the commit it was undoing and the branch's actual
// state: undoClaim reports whether its own `reset --soft`, the first of its
// steps, completed before a later, per-path step failed. When it did, that
// commit (when this round made one at all - stageAndCommitPaths may have
// found nothing of paths to stage) is off the branch either way, but a path
// undoClaim did not get to revert may still need finishing by hand; when
// the reset itself is what failed - on a ref or index lock another process
// on the clone holds, or a sha that no longer resolves - the commit is
// still the branch's HEAD, not off it, and there is nothing left for Claim
// itself to do but tell the operator which case it is. Otherwise nothing
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
				_, _ = s.undoClaim(pre, paths)
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
			if resetRan, err := s.undoClaim(pre, paths); err != nil {
				return "", undoFailedError(err, claimSHA, pre, resetRan)
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
//
// It reports resetRan: whether its first step, `reset --soft`, itself
// completed, before any error from a later step. A caller composing a
// refusal from a failed undoClaim needs that to tell the two failure shapes
// apart: resetRan false means the branch was never moved (sha, if this
// round made a commit at all, is still its HEAD), while resetRan true means
// the branch is already back at the pre-claim commit and a later, per-path
// step failed instead (see undoFailedError).
func (s *Store) undoClaim(sha string, paths []string) (resetRan bool, err error) {
	if _, err := gitx.Run(s.Root, "reset", "--soft", sha); err != nil {
		return false, err
	}
	for _, p := range paths {
		existed, err := gitx.PathExistsAtRev(s.Root, sha, p)
		if err != nil {
			return true, err
		}
		if existed {
			if _, err := gitx.Run(s.Root, "restore", "--source="+sha, "--staged", "--worktree", "--", p); err != nil {
				return true, err
			}
		} else {
			if _, err := gitx.Run(s.Root, "rm", "-r", "--cached", "--", p); err != nil {
				return true, err
			}
		}
	}
	args := append([]string{"clean", "-fdx", "--"}, paths...)
	_, err = gitx.Run(s.Root, args...)
	return true, err
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

// undoFailedError wraps a failed undoClaim as ID_NOT_CLAIMED too, describing
// what undoClaim actually left in the store rather than assuming its
// documented happy path always holds. claimSHA is the commit Claim read as
// HEAD right before calling undoClaim(pre, ...); resetRan is undoClaim's own
// report of whether its first step, `reset --soft pre`, completed before the
// step that failed; committed (claimSHA != pre) reports whether
// stageAndCommitPaths actually made a commit this round at all (it may have
// found nothing of paths to stage, leaving claimSHA at the pre-write HEAD).
// The four combinations say four different, mutually exclusive things:
//   - !resetRan, committed: the reset itself failed, so claimSHA is still
//     the branch's HEAD, with its ticket folder and record still committed
//   - the opposite of "off the branch".
//   - !resetRan, !committed: the reset itself failed, but this round made no
//     commit to begin with, so the branch should still be exactly at
//     claimSHA (== pre) regardless.
//   - resetRan, !committed: the reset succeeded (a no-op: pre was already
//     HEAD), but a later, per-path step failed cleaning up after a push
//     that was rejected without this round ever committing anything, so
//     claimSHA names a commit that predates this attempt, not "its rejected
//     commit".
//   - resetRan, committed: undoClaim's documented happy path - the reset
//     already moved claimSHA off the branch before a later step failed.
func undoFailedError(cause error, claimSHA, pre string, resetRan bool) error {
	if claimSHA == "" {
		claimSHA = "unknown"
	}
	committed := claimSHA != pre

	if !resetRan {
		if !committed {
			return &axi.Error{
				Msg:  fmt.Sprintf("the id could not be claimed on the store's origin, and cleaning up after the rejected push did not finish: resetting the branch failed: %v", cause),
				Code: idNotClaimedCode,
				Help: []string{fmt.Sprintf("This round made no commit of its own, so the branch should still be at %s: `git reset --soft` failed there, likely on a ref or index lock another process holds; clear that, confirm with `git status`, then retry.", claimSHA)},
			}
		}
		return &axi.Error{
			Msg:  fmt.Sprintf("the id could not be claimed on the store's origin, and undoing its rejected commit %s did not finish: resetting the branch back to %s failed: %v", claimSHA, pre, cause),
			Code: idNotClaimedCode,
			Help: []string{fmt.Sprintf("Commit %s is still the branch's HEAD, with its ticket folder and record still committed: `git reset --soft %s` failed there, likely on a ref or index lock another process holds, or on %s no longer resolving; clear that, confirm with `git status`, then retry.", claimSHA, pre, pre)},
		}
	}
	if !committed {
		return &axi.Error{
			Msg:  fmt.Sprintf("the id could not be claimed on the store's origin, and cleaning up after the rejected push did not finish: %v", cause),
			Code: idNotClaimedCode,
			Help: []string{fmt.Sprintf("This round made no commit of its own: the branch is already back at %s, a commit that predates this attempt. Check `git status` there for one of this claim's own paths left behind by the failed cleanup, finish by hand, then retry.", claimSHA)},
		}
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("the id could not be claimed on the store's origin, and reverting its rejected commit %s did not finish: %v", claimSHA, cause),
		Code: idNotClaimedCode,
		Help: []string{fmt.Sprintf("Commit %s is off the branch but still in the store: inspect it with `git show %s`, check `git status` for a path left partially reverted, finish by hand, then retry.", claimSHA, claimSHA)},
	}
}

// localChangesOverwrittenPattern matches git's own text for a merge or
// rebase that refuses because it would clobber an uncommitted local change:
// "...would be overwritten by merge:" or "...by rebase:", each followed by
// one tab-indented path per line. pullAfterUndoError keys off this
// structural shape, not a guess, so it blames a local change only when git's
// own message says one is actually in the way.
var localChangesOverwrittenPattern = regexp.MustCompile(`would be overwritten by (?:merge|rebase):\r?\n((?:\t[^\r\n]+\r?\n?)+)`)

// localChangesOverwritten returns the paths git's own text in cause names as
// the local changes a merge or rebase would overwrite, or nil when cause
// does not carry that shape.
func localChangesOverwritten(cause error) []string {
	m := localChangesOverwrittenPattern.FindStringSubmatch(cause.Error())
	if m == nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(strings.TrimRight(m[1], "\r\n"), "\n") {
		paths = append(paths, strings.TrimSpace(line))
	}
	return paths
}

// pullAfterUndoError wraps a failed post-undo pull as ID_NOT_CLAIMED. cp.pull
// (store.go) already carries its own best diagnosis in cause: a rebase that
// actually conflicted is already an *axi.Error, STORE_CONFLICT
// (abortFailedPull), with its own Code and the Help that resolves it - the
// conflicted paths, the exact rebase recipe, or, when jig's own best-effort
// `rebase --abort` also failed, that the store is left mid-rebase - and that
// error is returned unchanged rather than re-wrapped: replacing its Code and
// Help with ID_NOT_CLAIMED's "commit or stash it by hand" would send an
// operator whose store is stuck mid-rebase after instructions that neither
// finish nor abandon it. For any other cause - the fetch itself failing on
// an unreachable or moved origin, an auth failure, a dead network, or a
// local change blocking a plain fast-forward - a local change is named only
// when localChangesOverwritten actually finds one in cause's own text;
// otherwise cause is reported as what it is, rather than blamed on every
// dirty path the store happens to hold, most of which have nothing to do
// with this pull.
func pullAfterUndoError(root string, cause error) error {
	var ae *axi.Error
	if errors.As(cause, &ae) {
		return cause
	}
	if paths := localChangesOverwritten(cause); len(paths) > 0 {
		where := strings.Join(paths, ", ")
		return &axi.Error{
			Msg:  fmt.Sprintf("the id could not be claimed: pulling in the commit that rejected it failed, blocked by %s: %v", where, cause),
			Code: idNotClaimedCode,
			Help: []string{fmt.Sprintf("The store at %s still holds a local change in %s that the pull would overwrite: commit or stash it by hand there, then retry.", root, where)},
		}
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("the id could not be claimed: pulling in the commit that rejected it failed: %v", cause),
		Code: idNotClaimedCode,
		Help: []string{"Retry once the cause above is resolved."},
	}
}
