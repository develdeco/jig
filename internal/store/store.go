// Package store implements the jig truth-repo store: the git-backed tree of
// ticket folders, slice state, questions and the locked/atomic file
// primitives every writer in jig builds on.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
)

// Store is a truth-repo checkout rooted at Root.
type Store struct {
	Root string

	// AfterCheckpoint, when set, runs once after every Push that actually
	// lands (committed, and pushed when the store has an origin): the
	// GitHub mirror's full sync hooks in here (internal/mirror), so the
	// mirror depends on the store and never the reverse. A failure never
	// fails or stops the command whose Push found it: Push reports it
	// through Warn instead and returns nil, the same as every other
	// checkpoint. nil runs no hook.
	AfterCheckpoint func(s *Store) error

	// Warn reports AfterCheckpoint's failure, naming its cause. nil writes
	// "warning: <cause>" to os.Stderr. A command that wants the warning in
	// its own structured output sets this before calling Push.
	Warn func(format string, args ...any)

	// aliasClaimsCache memoizes aliasClaims' store-wide scan (internal/store/resolve.go)
	// for the life of this Store value: a command resolving many refs in one
	// run (jig validate's blocked_by refs and cycle DFS, the mirror's
	// blocker links and "Waits for" line) would otherwise re-read every
	// ticket.yaml once per ref. invalidateAliasClaims clears it whenever a
	// ticket.yaml write could change what it holds.
	aliasClaimsCache map[string][]string
}

// invalidateAliasClaims drops the memoized aliasClaims result, so the next
// call rescans the store. CreateTicketRecord and mutateTicket, the only
// writers of ticket.yaml, call this after a successful write: either one can
// change a ticket's aliases (or, for CreateTicketRecord, add a ticket whose
// id now claims itself) and so change what aliasClaims resolves.
func (s *Store) invalidateAliasClaims() {
	s.aliasClaimsCache = nil
}

// RunCheckpointHook runs AfterCheckpoint, when set, after a checkpoint has
// landed, reporting any failure through Warn (or its default) rather than
// propagating it: a sync failure (network, token, a GitHub error, a
// timeout) must never fail or stop the command that checkpointed. Push
// calls this itself after every checkpoint it makes; Claim does not, since
// the mirror's own claims (internal/mirror's claimIssue) reach the store
// through Claim too, and hooking Claim would re-enter the mirror. A command
// whose only checkpoint is a Claim, such as `jig ticket new`, calls this
// explicitly once the claim lands.
func (s *Store) RunCheckpointHook() {
	if s.AfterCheckpoint == nil {
		return
	}
	if err := s.AfterCheckpoint(s); err != nil {
		warn := s.Warn
		if warn == nil {
			warn = func(format string, args ...any) { fmt.Fprintf(os.Stderr, "warning: "+format+"\n", args...) }
		}
		warn("the tracker sync after this checkpoint failed: %v", err)
	}
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
	_, err := gitx.Run(s.Root, "remote", "get-url", "origin")
	return err == nil
}

// Sync stages and commits any uncommitted store state left behind by an
// earlier command that failed after writing to the store - for example a
// gate that appended its gate-open journal line and then failed at an
// oracle, or failed on SLICE_ID_DUPLICATE after writing its round - then
// pulls with rebase when a remote exists. Without this, a dirty tree makes
// every later command's Sync fail with "cannot pull with rebase", wedging
// the store until someone commits by hand. Committing the leftovers does
// not retry the failed command's round: Sync records them as a jig commit
// and the command proceeds, but a partial gate round directory still counts
// as a round, so the next `jig gate` opens round N+1 rather than replaying
// the failed one. The unfinished-rebase-or-merge and detached-HEAD refusals
// both run first, ahead of the no-remote early return, so a standalone
// store (no origin) in either state is still refused at the start of the
// command rather than silently let through. Otherwise it is a silent no-op
// when there is no remote. Callers run it at the start of every command.
func (s *Store) Sync() error {
	cp, err := s.begin()
	if err != nil {
		return err
	}
	if !cp.hasRemote() {
		return nil
	}
	if err := cp.commit("jig: record uncommitted store state"); err != nil {
		return err
	}
	return cp.pull()
}

// checkpoint is one Sync's or Push's hold on the store after its refusals
// have run. It works on the store in process (gitx.Repo) and falls back to
// the git program for any step the in-process repo declines with
// gitx.ErrUseCLI: a store that declares something gitx does not honor, a
// network remote, histories that diverged, a lock another process holds.
type checkpoint struct {
	s      *Store
	repo   *gitx.Repo // nil: the git program does every step
	branch string
}

// begin opens the store and runs the refusals every Sync and Push starts
// with, in this order: an unfinished rebase, merge, cherry-pick, revert,
// sequence or bisect, then unmerged index entries, then a detached HEAD.
func (s *Store) begin() (*checkpoint, error) {
	repo, err := gitx.OpenRepo(s.Root)
	if err != nil && !errors.Is(err, gitx.ErrUseCLI) {
		return nil, err
	}
	if repo != nil {
		cp, err := s.beginInProcess(repo)
		if !errors.Is(err, gitx.ErrUseCLI) {
			return cp, err
		}
	}
	if err := s.refuseIfMidRebaseOrMerge(); err != nil {
		return nil, err
	}
	branch, err := s.currentBranch()
	if err != nil {
		return nil, err
	}
	return &checkpoint{s: s, branch: branch}, nil
}

// beginInProcess runs begin's refusals through repo, or returns
// gitx.ErrUseCLI for the git program to run them.
func (s *Store) beginInProcess(repo *gitx.Repo) (*checkpoint, error) {
	markers := make([]string, len(rebaseOrMergeMarkers))
	for i, m := range rebaseOrMergeMarkers {
		markers[i] = repo.GitPath(m.gitPath)
	}
	what, err := markerPresent(markers)
	if err != nil {
		return nil, err
	}
	var st gitx.RepoState
	if what == "" {
		if st, err = repo.State(); err != nil {
			return nil, err
		}
		if st.Unmerged {
			what = unmergedDescription
		}
	}
	if what != "" {
		return nil, s.conflictError(what)
	}
	if st.Branch == "" {
		return nil, s.detachedHEADError()
	}
	return &checkpoint{s: s, repo: repo, branch: st.Branch}, nil
}

// hasRemote reports whether the store has an origin remote.
func (cp *checkpoint) hasRemote() bool {
	if cp.repo != nil {
		return cp.repo.HasRemote("origin")
	}
	return cp.s.HasRemote()
}

// commit records every change with msg, when there is any.
func (cp *checkpoint) commit(msg string) error {
	if cp.repo != nil {
		_, err := cp.repo.CommitAll(msg, "jig", "jig@invalid")
		if !errors.Is(err, gitx.ErrUseCLI) {
			return err
		}
	}
	_, err := cp.s.stageAndCommit(msg)
	return err
}

// pull brings origin's branch in, as `git pull --rebase` would: fetched in
// process, then nothing more when the store is level with or ahead of it,
// the git program's fast-forward when it is behind, and its rebase, with
// its conflict handling (abortFailedPull), when the two diverged.
func (cp *checkpoint) pull() error {
	if cp.repo != nil {
		rel, err := cp.repo.Fetch("origin", cp.branch)
		switch {
		case errors.Is(err, gitx.ErrUseCLI):
		case err != nil:
			return err
		case rel == gitx.Same, rel == gitx.Ahead:
			return nil
		case rel == gitx.Behind:
			_, err := gitx.Run(cp.s.Root, "merge", "--ff-only", "--quiet", "refs/remotes/origin/"+cp.branch)
			return err
		}
	}
	if _, err := gitx.Run(cp.s.Root, "pull", "--rebase", "origin", cp.branch); err != nil {
		return cp.s.abortFailedPull(err, cp.branch)
	}
	return nil
}

// push pushes the branch to origin, and on a rejection pulls (rebasing onto
// what moved on) and pushes once more.
func (cp *checkpoint) push() error {
	if cp.repo != nil {
		err := cp.repo.Push("origin", cp.branch)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, gitx.ErrPushRejected):
			if err := cp.pull(); err != nil {
				return err
			}
			if err := cp.repo.Push("origin", cp.branch); !errors.Is(err, gitx.ErrUseCLI) {
				return err
			}
		case !errors.Is(err, gitx.ErrUseCLI):
			return err
		}
	}
	if _, err := gitx.Run(cp.s.Root, "push", "origin", cp.branch); err != nil {
		if _, perr := gitx.Run(cp.s.Root, "pull", "--rebase", "origin", cp.branch); perr != nil {
			return cp.s.abortFailedPull(perr, cp.branch)
		}
		if _, err2 := gitx.Run(cp.s.Root, "push", "origin", cp.branch); err2 != nil {
			return err2
		}
	}
	return nil
}

// pushOnce pushes the branch to origin exactly once, with none of push's own
// retry: Claim needs to tell a push the origin rejected (not a fast-forward:
// something else landed there first) apart from any other failure, since
// only a rejection is worth pulling and preparing a fresh claim for.
func (cp *checkpoint) pushOnce() (rejected bool, err error) {
	if cp.repo != nil {
		err := cp.repo.Push("origin", cp.branch)
		switch {
		case err == nil:
			return false, nil
		case errors.Is(err, gitx.ErrPushRejected):
			return true, err
		case !errors.Is(err, gitx.ErrUseCLI):
			return false, err
		}
	}
	out, err := gitx.RunRaw(cp.s.Root, "push", "origin", cp.branch)
	if err == nil {
		return false, nil
	}
	return pushRejectedText(out), err
}

// pushRejectedText reports whether out - git push's combined stdout and
// stderr - names a rejection (the remote branch moved on; not a
// fast-forward), the only push failure Claim retries rather than refusing
// outright.
func pushRejectedText(out string) bool {
	return strings.Contains(out, "[rejected]") ||
		strings.Contains(out, "non-fast-forward") ||
		strings.Contains(out, "fetch first")
}

// refuseIfMidRebaseOrMerge errors when the store has an unfinished rebase or
// merge in progress, or unresolved conflict markers already sitting in the
// index, without touching the index itself. Neither Sync nor Push must ever
// stage or commit over that: `git add -A` would pick up unresolved conflict
// markers, and a later `rebase --continue` (or manual resolution) would
// then commit them onto the store branch, corrupting whatever file
// conflicted (e.g. journal.ndjson) for every later reader. Both Sync and
// Push call it before anything else, so it guards a command that only ever
// Pushes (e.g. `jig requeue`) too - not only the commands that Sync first.
func (s *Store) refuseIfMidRebaseOrMerge() error {
	what, err := inProgressRebaseOrMerge(s.Root)
	if err != nil {
		return err
	}
	if what == "" {
		return nil
	}
	return s.conflictError(what)
}

// conflictError is the STORE_CONFLICT for what (a rebaseOrMergeMarkers
// description, or unmergedDescription).
func (s *Store) conflictError(what string) error {
	return &axi.Error{
		Msg:  fmt.Sprintf("the store at %s has %s", s.Root, what),
		Code: "STORE_CONFLICT",
		Help: []string{"Check the store's state there with `git status`, resolve it, then rerun."},
	}
}

// rebaseOrMergeMarkers pairs each git-path marker inProgressRebaseOrMerge
// checks for with the description it reports when that marker is present.
// Keeping flag and description in one table, rather than two indexed in
// parallel, means the two cannot drift apart: a marker added here without
// a description is a compile error, not an index-out-of-range panic inside
// a guard that runs on every Sync and Push.
var rebaseOrMergeMarkers = []struct {
	gitPath     string
	description string
}{
	{"rebase-merge", "an unfinished rebase"},
	{"rebase-apply", "an unfinished rebase"},
	{"MERGE_HEAD", "an unfinished merge"},
	{"CHERRY_PICK_HEAD", "an unfinished cherry-pick"},
	{"REVERT_HEAD", "an unfinished revert"},
	{"sequencer", "a cherry-pick or revert sequence"},
	{"BISECT_LOG", "an unfinished bisect"},
}

// inProgressRebaseOrMerge reports which of an unfinished rebase (git leaves
// a rebase-merge or rebase-apply directory under .git for the duration of
// one), an unresolved merge (MERGE_HEAD), an unfinished cherry-pick or
// revert (CHERRY_PICK_HEAD or REVERT_HEAD - git keeps these set even once
// the conflict is resolved and staged, until `--continue` or `--abort`
// runs), a multi-commit cherry-pick or revert sequence (the sequencer
// directory), an unfinished bisect (BISECT_LOG), or unmerged index entries
// left by something other than any of those - a conflicted `git stash pop`
// leaves the index with conflict markers on disk but none of these markers
// - dir is in, as a short description naming the one that matched, or ""
// when none is present. The git-path markers (rebaseOrMergeMarkers) are
// read with one `rev-parse` call rather than one per marker, since this
// runs on every Sync and every Push. The unmerged-index check uses
// `git ls-files -u`, which only reads the index, rather than
// `git diff --diff-filter=U`, which opportunistically rewrites .git/index
// as a side effect - a write this read-only guard, called on every Sync
// and Push, must not make.
func inProgressRebaseOrMerge(dir string) (string, error) {
	args := make([]string, 0, 1+2*len(rebaseOrMergeMarkers))
	args = append(args, "rev-parse")
	for _, m := range rebaseOrMergeMarkers {
		args = append(args, "--git-path", m.gitPath)
	}
	out, err := gitx.Run(dir, args...)
	if err != nil {
		return "", err
	}
	markers := make([]string, len(rebaseOrMergeMarkers))
	for i, line := range strings.Split(out, "\n") {
		if i >= len(markers) {
			break
		}
		p := strings.TrimSpace(line)
		if p != "" && !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		markers[i] = p
	}
	if what, err := markerPresent(markers); err != nil || what != "" {
		return what, err
	}
	unmerged, err := gitx.Run(dir, "ls-files", "-u")
	if err != nil {
		return "", err
	}
	if unmerged != "" {
		return unmergedDescription, nil
	}
	return "", nil
}

// unmergedDescription is what a refusal reports for unmerged index entries.
const unmergedDescription = "unresolved (unmerged) index entries"

// markerPresent returns the description of the first rebaseOrMergeMarkers
// entry whose path (markers, in table order; "" to skip) exists, or "".
func markerPresent(markers []string) (string, error) {
	for i, p := range markers {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return rebaseOrMergeMarkers[i].description, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", nil
}

// stageAndCommit stages every change (`add -A`) and, when anything is
// staged, commits it with jig's identity and msg. It reports whether a
// commit was made. Its callers, Sync and Push, have already run
// refuseIfMidRebaseOrMerge, and only read-only git calls run between that
// check and this one, so it does not repeat the check's two git calls.
// Another process working on the same store can still slip in between, as
// it always could between the check and `add -A`.
func (s *Store) stageAndCommit(msg string) (bool, error) {
	if _, err := gitx.Run(s.Root, "add", "-A"); err != nil {
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

// Push stages every change, commits it (skipping the commit when nothing is
// staged) and, when a remote exists, pushes it, retrying once with a
// pull --rebase on rejection. Like Sync, it refuses on an unfinished rebase
// or merge or a detached HEAD before doing anything else, whether or not a
// remote exists.
func (s *Store) Push(msg string) error {
	cp, err := s.begin()
	if err != nil {
		return err
	}
	if err := cp.commit(msg); err != nil {
		return err
	}
	if !cp.hasRemote() {
		s.RunCheckpointHook()
		return nil
	}
	if err := cp.push(); err != nil {
		return err
	}
	// Keep the long-lived store packed. Best-effort: a maintenance failure
	// (for example a lock held by the user's own maintenance) must not fail
	// a push that already succeeded. In process, the git program starts
	// only once the loose objects may have passed git's own gc.auto limit.
	if cp.repo == nil || cp.repo.LooseObjectsPastAutoGC() {
		_ = gitx.MaintenanceAuto(s.Root)
	}
	s.RunCheckpointHook()
	return nil
}

// abortFailedPull handles jig's own failed `pull --rebase` on branch. A pull
// that stopped on a conflict leaves a rebase in progress (Sync and Push
// refused any rebase or merge that was already there, so this one is jig's
// own, unless another process working on the same store started one in
// between, a race recorded in DECISIONS.md): the conflicting paths are read
// structurally (`git ls-files -u`, before anything else touches the index)
// so the report can name them, then the rebase is aborted with a best-effort
// `rebase --abort`, and the result reported as STORE_CONFLICT either way,
// since the store is still mid-rebase if the abort itself failed (for
// example a Windows file lock) and needs the same manual resolution. A pull
// that failed before rebasing (an unreachable or moved remote, an auth
// failure) left nothing to abort, so its error is returned unchanged rather
// than misreported as a conflict. When the state cannot be read, the abort
// is still attempted (best effort), and the read's own error is carried into
// the wrapped message rather than assumed away.
func (s *Store) abortFailedPull(pullErr error, branch string) error {
	mid, stateErr := inProgressRebaseOrMerge(s.Root)
	if stateErr == nil && mid == "" {
		return pullErr
	}
	paths, _ := conflictedPaths(s.Root)
	_, abortErr := gitx.Run(s.Root, "rebase", "--abort")
	var sha string
	if abortErr == nil {
		sha, _ = gitx.Run(s.Root, "rev-parse", "HEAD")
	}
	return s.wrapAbortedPullConflict(abortErr, stateErr, paths, sha, branch)
}

// conflictedPaths returns the unique paths with unmerged (conflicted) index
// entries in dir, in the order `git ls-files -u` lists them. Each conflicted
// path appears once per stage (1/2/3) in that output; this collapses them to
// one entry per path. Like inProgressRebaseOrMerge's own check, it reads
// only the index and never rewrites it.
func conflictedPaths(dir string) ([]string, error) {
	out, err := gitx.Run(dir, "ls-files", "-u")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		i := strings.IndexByte(line, '\t')
		if i < 0 {
			continue
		}
		p := line[i+1:]
		if seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}
	return paths, nil
}

// wrapAbortedPullConflict turns a failed pull --rebase into a STORE_CONFLICT
// the operator can act on, after jig's own best-effort `rebase --abort` has
// run (abortErr is that attempt's result, nil on success) and after the
// state read that decided whether to attempt it (stateErr, non-nil when
// inProgressRebaseOrMerge itself could not read the store's state). The
// message is built entirely from state jig itself read - the conflicting
// paths (read before the abort) and, once aborted, the commit the store
// landed back on - never from git's stderr: that text carries `rebase
// --continue`/`--skip`/`--abort` hints for the rebase jig has just aborted,
// which fail if followed as printed. When the abort itself failed, the
// message says so plainly instead of falsely claiming the rebase was
// aborted. If the state read had also failed, the message does not assert
// the store is mid-rebase either - that was never confirmed - and instead
// says the store's state is unknown, pointing at `git status` in the store
// rather than at a specific rebase to abort or continue.
func (s *Store) wrapAbortedPullConflict(abortErr, stateErr error, paths []string, sha, branch string) error {
	if abortErr != nil {
		if stateErr != nil {
			return &axi.Error{
				Msg:  fmt.Sprintf("the store at %s: pull --rebase of origin/%s failed, its state could not be read (%v), and the best-effort `rebase --abort` also failed: %v", s.Root, branch, stateErr, abortErr),
				Code: "STORE_CONFLICT",
				Help: []string{fmt.Sprintf("The store at %s is in an unknown state: check it there with `git status`, then resolve whatever it shows.", s.Root)},
			}
		}
		return &axi.Error{
			Msg:  fmt.Sprintf("the store at %s: pull --rebase of origin/%s conflicted, and the best-effort `rebase --abort` also failed: %v", s.Root, branch, abortErr),
			Code: "STORE_CONFLICT",
			Help: []string{fmt.Sprintf("The store at %s is still mid-rebase: resolve it there with `git status`, then `git rebase --abort` or `--continue`.", s.Root)},
		}
	}
	where := "an unrecorded path"
	if len(paths) > 0 {
		where = strings.Join(paths, ", ")
	}
	at := sha
	if at == "" {
		at = "unknown"
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("the store at %s: pull --rebase of origin/%s conflicted in %s and was aborted by jig; the store is back at its pre-pull commit %s", s.Root, branch, where, at),
		Code: "STORE_CONFLICT",
		Help: []string{fmt.Sprintf("In the store at %s: `git pull --rebase origin %s`, fix the conflict in %s, `git add` it, `git rebase --continue`, then rerun this command.", s.Root, branch, where)},
	}
}

// currentBranch returns the checked-out branch name, refusing with
// STORE_CONFLICT when HEAD is detached (for example mid-`git bisect`):
// `git symbolic-ref -q --short HEAD` fails exactly when HEAD does not point
// at a branch, unlike `rev-parse --abbrev-ref HEAD`, which happily prints
// the literal "HEAD" and lets Sync or Push run `pull`/`push` against it.
func (s *Store) currentBranch() (string, error) {
	branch, err := gitx.Run(s.Root, "symbolic-ref", "-q", "--short", "HEAD")
	if err != nil {
		return "", s.detachedHEADError()
	}
	return branch, nil
}

// detachedHEADError is the STORE_CONFLICT for a HEAD that points at no
// branch.
func (s *Store) detachedHEADError() error {
	return &axi.Error{
		Msg:  fmt.Sprintf("the store at %s has a detached HEAD, not a branch", s.Root),
		Code: "STORE_CONFLICT",
		Help: []string{"Check the store's state there with `git status`, resolve it, then rerun."},
	}
}

// hasStagedChanges reports whether the index differs from HEAD, restricted
// to paths when any are given or the whole index when none are: "diff
// --cached --quiet [-- <paths>]" exits 1 for staged changes; any other
// failure is an error.
func (s *Store) hasStagedChanges(paths ...string) (bool, error) {
	args := []string{"diff", "--cached", "--quiet"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	_, err := gitx.Run(s.Root, args...)
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, err
}

// Dirty reports whether the working tree has anything uncommitted - staged,
// unstaged, or untracked - via "status --porcelain", without staging or
// committing anything itself. A caller uses this to decide whether it is
// worth calling Push at all: for example a command whose own write already
// landed on disk in an earlier, failed run (Push having refused before its
// stageAndCommit ever ran) can retry the commit only when there is one
// still pending, rather than attempting a Push - and, when a remote exists,
// its network round trip - on every call.
func (s *Store) Dirty() (bool, error) {
	out, err := gitx.Run(s.Root, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// ID returns a short identifier for this store clone that is stable on this
// machine: the first 16 hex digits of the sha256 of the store's root,
// absolute and with symlinks resolved (Windows spells the resolved path in
// its on-disk case), so the same clone reached by a relative path, a symlink
// or a differently cased spelling names one id. A Windows junction is not a
// symlink and is not resolved: a clone reached through one names its own id,
// as a clone moved elsewhere does. It keys the machine-local files
// that belong to a store but must stay out of its git, such as a ticket's
// recordings under the jig home's evidence tree (home.RecordDir). It is
// derived from the path, not stored, because those files are per machine like
// the clone itself: a clone moved elsewhere is a new id, and its media are not
// found under the old one.
func (s *Store) ID() (string, error) {
	abs, err := filepath.Abs(s.Root)
	if err != nil {
		return "", fmt.Errorf("store: resolve %s: %w", s.Root, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("store: resolve %s: %w", s.Root, err)
	}
	sum := sha256.Sum256([]byte(resolved))
	return hex.EncodeToString(sum[:8]), nil
}
