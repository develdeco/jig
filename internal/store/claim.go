package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
// merely for holding no tracked files). It undoes only once it has confirmed
// the undo is safe, though: undoClaim's own `reset --soft` moves the branch
// back to the commit from before this round's write ran, and cannot discard
// this round's own commit alone - it takes whatever else sits between the
// two with it, which would silently throw a commit another jig process made
// on this same clone while this claim was in flight (Store.Sync's or
// Store.Push's own `add -A`, racing in before or after this round's own
// commit) off the branch - its message, author and date gone, recoverable
// afterwards only from that clone's own reflog. foreignCommitAhead checks
// for exactly that before every undo; when it finds one, Claim never resets
// at all, refusing at once with ID_NOT_CLAIMED naming the other commit
// (foreignCommitError) and leaving the branch untouched for the operator to
// reconcile by hand. Once the undo is confirmed safe, Claim pulls and calls
// write again, up to maxClaimAttempts times. That pull is a fast-forward in
// the common case (the undone commit was the only thing the local branch had
// that the origin lacked), but not always: a local, uncommitted change
// undoClaim's scoped undo preserved (another process's own edit, still in
// the store) can overlap a path the rejecting push just brought in, and when
// it does the pull refuses rather than merging or rebasing. Claim reports
// that as ID_NOT_CLAIMED too, naming the local change only when the pull's
// own failure actually names one - git's "would be overwritten by merge"
// text, or a rebase conflict's own STORE_CONFLICT (abortFailedPull), which
// Claim returns unchanged, Code and Help both, since that already carries
// the real recipe; anything else - an unreachable or moved origin, a dead
// network - is reported as what it is, not blamed on a file that was never
// in the way. A push that fails any other way (the origin unreachable, or
// anything else) undoes the claim the same way, subject to the same safety
// check, and refuses at once with ID_NOT_CLAIMED, since retrying blind would
// not help; so does the last of maxClaimAttempts rejections. An undo that
// does not finish is reported the same way too, naming the commit it was
// undoing and the branch's actual state: undoClaim reports whether its own
// `reset --soft`, the first of its steps, completed before a later, per-path
// step failed. When it did, that
// commit (when this round made one at all - stageAndCommitPaths may have
// found nothing of paths to stage) is off the branch either way, but a path
// undoClaim did not get to revert may still need finishing by hand; when
// the reset itself is what failed - on a ref or index lock another process
// on the clone holds, or a sha that no longer resolves - the commit is
// still the branch's HEAD, not off it, and there is nothing left for Claim
// itself to do but tell the operator which case it is. Otherwise nothing
// write wrote survives a failed Claim: no caller ever has to clean up a
// folder or a file edit Claim itself could not land.
//
// A write that fails returns its paths too: paths are meaningful whatever
// the error is, and list every path the write created or edited so far -
// only what it touched, because the undo restores a path that existed before
// the claim to that commit's content and removes one that did not, so a
// reported path the write never touched would lose a person's own
// uncommitted edit. Claim then undoes exactly those paths (undoPaths; no
// reset, nothing was committed) and returns the write's own error. A write
// that fails before touching anything returns no paths and nothing is undone.
// A commit that fails after write is undone the same way, and its own error
// comes back (unwrapping through an incomplete undo's report too).
// Every undo is best effort: it tries each step for each path and the final
// clean, and one that does not finish returns an error naming the leftover
// paths and how to finish by hand, which unwraps to the write's own error
// (a failed write) or stays ID_NOT_CLAIMED (a push); IsUndoIncomplete tells
// either shape apart through any wrapping.
//
// Claim holds the checkpoint lock (LockCheckpoint) for the whole of it,
// every retry included, so no other process's Sync or Push, and no journal
// append, runs between its commit and its undo.
//
// In a writers store (Mode) the loop is the same one on the branch of this
// writer, writers/<name>, which only this writer pushes to: what could make
// its push fail is another clone writing under the same name (the pull that
// follows refuses it with WRITER_NAME_TAKEN), not another writer minting the
// same id. Ids are kept apart by the claims log instead: Mint reserves the id
// there before write has made its folder (mint.go, claims.go), so by the time
// the commit is pushed the id is already this writer's.
func (s *Store) Claim(write func() (id string, paths []string, err error), msg func(id string) string) (string, error) {
	release, err := s.LockCheckpoint()
	if err != nil {
		return "", err
	}
	defer release()
	cp, err := s.begin()
	if err != nil {
		return "", err
	}
	// A claim always commits, so a clone with no identity is refused before
	// write puts anything on disk, not after.
	if _, err := s.identity(); err != nil {
		return "", err
	}
	hasRemote := cp.hasRemote()

	for attempt := 1; attempt <= maxClaimAttempts; attempt++ {
		pre, havePre := s.headSHA()

		directDone := s.DirectTrackerWrites()
		id, paths, err := write()
		directDone()
		if err != nil {
			return "", s.undoFailedWrite(err, pre, havePre, paths)
		}
		claimSHA, err := s.stageAndCommitPaths(paths, msg(id))
		if err != nil {
			return "", s.undoFailedCommit(err, pre, havePre, paths)
		}
		if !hasRemote {
			return id, nil
		}

		rejected, perr := cp.pushOnce()
		if perr == nil {
			return id, nil
		}
		if havePre {
			foreign, ferr := s.foreignCommitAhead(pre, claimSHA)
			if ferr != nil {
				return "", idNotClaimedError(ferr)
			}
			if foreign != "" {
				return "", foreignCommitError(foreign)
			}
			effectiveSHA := claimSHA
			if effectiveSHA == "" {
				effectiveSHA = pre
			}
			if resetRan, err := s.undoClaim(pre, paths); err != nil {
				return "", withLeftovers(undoFailedError(err, effectiveSHA, pre, resetRan), nil, err, s.Root)
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
	sha, found, err := gitx.Resolve(s.Root, "HEAD")
	if err != nil || !found {
		return "", false
	}
	return sha, true
}

// foreignCommitAhead reports a commit, other than this round's own
// (claimSHA, as stageAndCommitPaths reported it - "" when it found nothing
// of paths to stage), that sits between pre and the branch's current tip:
// landed under claimSHA (its own parent is not pre - another process
// committed before this round's commit did) or over it (the tip is not
// claimSHA at all - another process committed after). It returns "" when
// pre..tip holds nothing but claimSHA alone, or, when claimSHA is "", when
// the tip is still pre itself - the one shape undoClaim's `reset --soft
// pre` is safe to run without rewinding over a commit that is not this
// claim's to discard (see Claim's own doc comment and foreignCommitError,
// which is what runs instead when it is not safe).
func (s *Store) foreignCommitAhead(pre, claimSHA string) (string, error) {
	tip, ok := s.headSHA()
	if !ok {
		return "", fmt.Errorf("HEAD does not resolve")
	}
	if tip == pre {
		return "", nil
	}
	if claimSHA != "" && tip == claimSHA {
		parent, err := gitx.Verify(s.Root, claimSHA+"^")
		if err != nil {
			return "", err
		}
		if parent == pre {
			return "", nil
		}
		return parent, nil
	}
	return tip, nil
}

// foreignCommitError refuses with ID_NOT_CLAIMED instead of ever calling
// undoClaim: foreignCommitAhead found a commit between pre and the branch's
// current tip that is not this round's own, and undoClaim's `reset --soft
// pre` cannot discard this round's rejected commit alone - it moves the
// branch back past whatever sits between the two, which would throw foreign
// off the branch exactly as silently as a hard reset would (its message,
// author and date gone, recoverable afterwards only from this clone's own
// reflog, not the branch). Claim leaves the branch, foreign, and this
// round's own rejected commit exactly as they are instead, for the operator
// to reconcile by hand.
func foreignCommitError(foreign string) error {
	return &axi.Error{
		Msg:  fmt.Sprintf("the id could not be claimed on the store's origin: commit %s landed on the branch while this claim was in flight, and undoing the rejected push would rewind the branch past it", foreign),
		Code: idNotClaimedCode,
		Help: []string{fmt.Sprintf("Claim left the branch untouched rather than rewinding over %s. Inspect it with `git log` and `git show %s`, reconcile the branch by hand (for example, rebase this claim's own rejected commit onto the current origin) and push, then retry.", foreign, foreign)},
	}
}

// undoClaim discards exactly the commit write's own call just made - never
// anything else in the store - how Claim undoes a push the origin rejected
// or otherwise refused, once Claim's own foreignCommitAhead check has
// confirmed that is safe: nothing but this round's own commit sits between
// sha (pre, in Claim's own terms) and the branch's current tip. `reset
// --soft` moves HEAD (and the branch) back to sha without touching the
// index or the working tree: an edit another process left uncommitted in
// the store (a journal.ndjson line) is untouched by it. A commit that
// process made of its own, racing in after foreignCommitAhead's check but
// before this reset runs, would still be orphaned by it exactly as
// foreignCommitAhead exists to prevent - no longer reachable from the
// branch, its content left staged against the rewound HEAD; it is
// stageAndCommitPaths' own pathspec-scoped commit, not this reset, that
// then keeps that content out of Claim's next retry (see
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
	return true, s.undoPaths(sha, paths)
}

// undoPaths puts paths back as they were at sha: a path that existed there is
// restored (index and working tree) to that commit's content, one that did not
// is taken out of the index (`rm -r --cached --ignore-unmatch`, so a path
// nothing staged is no error) and then cleaned off the disk with its ignored
// files (-x), such as a ticket folder's .lock sidecar. On an unborn branch (sha
// is "") nothing is restored, but each path is still taken out of the index. It is best effort: every step for every path and
// the final clean are tried even after one fails, and the errors come back
// joined, as an *undoPathsError naming the paths whose own step failed.
// Called with no paths it does nothing - `clean -fdx --` with no pathspec would
// clean the whole store.
func (s *Store) undoPaths(sha string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	// The paths may hold tracker records, which this takes off the disk behind
	// the index's back.
	defer s.invalidateTrackerIndex()
	var errs []error
	var leftovers []string
	fail := func(p string, err error) {
		errs = append(errs, err)
		leftovers = append(leftovers, p)
	}
	// created are the paths that did not exist at sha: the clean's to take.
	// On an unborn branch (sha is "") none existed, but the claim's own add may
	// have staged them, and clean skips what the index holds.
	var created []string
	for _, p := range paths {
		existed := false
		if sha != "" {
			var err error
			if existed, err = gitx.PathExistsAtRev(s.Root, sha, p); err != nil {
				fail(p, err)
				continue
			}
		}
		if existed {
			if _, err := gitx.Run(s.Root, "restore", "--source="+sha, "--staged", "--worktree", "--", p); err != nil {
				fail(p, err)
			}
			continue
		}
		created = append(created, p)
		if _, err := gitx.Run(s.Root, "rm", "-r", "--cached", "--ignore-unmatch", "--", p); err != nil {
			fail(p, err)
		}
	}
	// Only the outermost paths are cleaned: git clean leaves the emptied
	// directory behind when a file inside it is listed before the directory.
	args := append([]string{"clean", "-fdx", "--"}, outermost(paths)...)
	if _, err := gitx.Run(s.Root, args...); err != nil {
		errs = append(errs, err)
		// Only a path the clean was to take and that is still on disk is left
		// over: a restored one was never the clean's.
		for _, p := range created {
			if _, serr := os.Lstat(filepath.Join(s.Root, filepath.FromSlash(p))); serr == nil && !slices.Contains(leftovers, p) {
				leftovers = append(leftovers, p)
			}
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return &undoPathsError{err: errors.Join(errs...), leftovers: leftovers}
}

// outermost returns the paths that no other path in paths contains.
func outermost(paths []string) []string {
	var out []string
	for _, p := range paths {
		nested := false
		for _, q := range paths {
			if q != p && strings.HasPrefix(filepath.ToSlash(p), strings.TrimSuffix(filepath.ToSlash(q), "/")+"/") {
				nested = true
				break
			}
		}
		if !nested && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// undoPathsError is undoPaths' failure: the joined step errors, and the
// paths whose own step failed.
type undoPathsError struct {
	err       error
	leftovers []string
}

func (e *undoPathsError) Error() string { return e.err.Error() }
func (e *undoPathsError) Unwrap() error { return e.err }

// undoIncompleteError is what Claim returns when an undo did not finish. It
// renders as ae (an *axi.Error, found by errors.As) and, for a failed write,
// also unwraps to the write's own error (cause), so a caller's errors.Is and
// errors.As on its own sentinel keep working. leftovers are the paths the
// undo could not clear.
type undoIncompleteError struct {
	ae        *axi.Error
	cause     error
	leftovers []string
}

func (e *undoIncompleteError) Error() string { return e.ae.Msg }
func (e *undoIncompleteError) Unwrap() []error {
	if e.cause == nil {
		return []error{e.ae}
	}
	return []error{e.ae, e.cause}
}

// IsUndoIncomplete reports whether err (through any wrapping) is a Claim
// refusal whose undo did not finish: some path it wrote may still sit in the
// store, uncommitted, and the error names which.
func IsUndoIncomplete(err error) bool {
	var e *undoIncompleteError
	return errors.As(err, &e)
}

// withLeftovers wraps ae, the report of an undo that did not finish, as an
// undoIncompleteError. The leftover paths come from undoErr (an
// *undoPathsError); cause, when non-nil, is the write's own error. ae gains
// the leftover list and a help line to finish by hand.
func withLeftovers(ae *axi.Error, cause, undoErr error, root string) error {
	out := &axi.Error{Msg: ae.Msg, Code: ae.Code, Help: slices.Clone(ae.Help)}
	var leftovers []string
	var upe *undoPathsError
	if errors.As(undoErr, &upe) {
		leftovers = upe.leftovers
	}
	if len(leftovers) > 0 {
		list := strings.Join(leftovers, ", ")
		out.Msg += fmt.Sprintf(" (left in the store: %s)", list)
		out.Help = append(out.Help, fmt.Sprintf("Finish by hand in %s once nothing holds %s: `git restore --source=HEAD --staged --worktree -- <path>` for one the store already tracked, otherwise `git rm -r --cached --ignore-unmatch -- <path>` and `git clean -fdx -- <path>`.", root, list))
	}
	return &undoIncompleteError{ae: out, cause: cause, leftovers: leftovers}
}

// undoFailedWrite is Claim's answer to a write that returned err: write's
// paths are meaningful on error and list only what it touched, so exactly
// those are undone (nothing was committed, so there is no reset), and err comes
// back as it was. With no paths nothing runs. An undo that does not finish
// returns an error that unwraps to err and names what is left
// (IsUndoIncomplete).
func (s *Store) undoFailedWrite(err error, pre string, havePre bool, paths []string) error {
	if len(paths) == 0 {
		return err
	}
	if !havePre {
		pre = ""
	}
	uerr := s.undoPaths(pre, paths)
	return s.incompleteUndo(err, uerr)
}

// undoFailedCommit is Claim's answer to a commit that failed: the commit's
// error comes back as it was, after what write touched is undone (HEAD is
// still pre, so the reset is a no-op; on an unborn branch each path is
// unstaged and cleaned, since the commit's own add staged it). An undo that
// does not finish is reported as undoFailedWrite reports it.
func (s *Store) undoFailedCommit(err error, pre string, havePre bool, paths []string) error {
	var uerr error
	if havePre {
		_, uerr = s.undoClaim(pre, paths)
	} else {
		uerr = s.undoPaths("", paths)
	}
	return s.incompleteUndo(err, uerr)
}

// incompleteUndo returns err when uerr is nil, otherwise err's report that the
// undo did not finish: it unwraps to err, keeps err's own code and help, and
// names the leftover paths (IsUndoIncomplete).
func (s *Store) incompleteUndo(err, uerr error) error {
	if uerr == nil {
		return err
	}
	ae := &axi.Error{
		Msg:  fmt.Sprintf("%v; undoing what the write left behind did not finish: %v", err, uerr),
		Code: idNotClaimedCode,
	}
	var orig *axi.Error
	if errors.As(err, &orig) {
		ae.Code = orig.Code
		ae.Help = slices.Clone(orig.Help)
	}
	return withLeftovers(ae, err, uerr, s.Root)
}

// stageAndCommitPaths stages exactly paths (never -A) and, when anything of
// theirs is staged, commits as the person (Store.identity) - the commit
// itself scoped to the same pathspec (`git commit -- <paths>`), not the
// whole index.
// Unlike stageAndCommit, it leaves any other dirty state in the store
// untouched, so a claim's commit holds only what write reported: without
// the pathspec, a commit here would instead sweep in whatever else the
// index happens to carry staged - a narrow race (see foreignCommitAhead)
// can still leave another process's own commit staged exactly that way
// after undoClaim's `reset --soft` - under write's own message, losing that
// other commit's identity and pushing its content as if it were this
// claim's. It reports the new commit's sha, or "" when nothing of paths
// needed staging this round, so Claim's own caller can tell its commit
// apart from anything else that may land on the branch around it (see
// foreignCommitAhead).
func (s *Store) stageAndCommitPaths(paths []string, msg string) (string, error) {
	args := append([]string{"add", "--"}, paths...)
	if _, err := gitx.Run(s.Root, args...); err != nil {
		return "", err
	}
	staged, err := s.hasStagedChanges(paths...)
	if err != nil {
		return "", err
	}
	if !staged {
		return "", nil
	}
	id, err := s.identity()
	if err != nil {
		return "", err
	}
	commitArgs := commitConfig(id, append([]string{"commit", "-m", msg, "--"}, paths...)...)
	if _, err := gitx.Run(s.Root, commitArgs...); err != nil {
		return "", err
	}
	sha, ok := s.headSHA()
	if !ok {
		return "", fmt.Errorf("HEAD does not resolve right after committing")
	}
	return sha, nil
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
func undoFailedError(cause error, claimSHA, pre string, resetRan bool) *axi.Error {
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
