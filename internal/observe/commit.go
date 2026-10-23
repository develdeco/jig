package observe

import (
	"errors"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/verify"
)

// postCommit records the commit git just made and starts its verification.
// Its order is the order of the runner it replaced: HEAD, then the branch,
// then the ledger line, so a directory that is no repository fails at HEAD.
func (h *handler) postCommit() Result {
	cwd := h.ev.Cwd
	if cwd == "" {
		cwd = h.d.Cwd
	}
	if cwd == "" {
		h.fail(errors.New("observe: the event names no directory"))
		return Result{}
	}
	sha, err := gitx.RevParse(cwd, "HEAD")
	if err != nil {
		h.fail(err)
		return Result{}
	}
	// A detached HEAD has no branch: the line then carries none.
	branch, _ := gitx.Run(cwd, "branch", "--show-current")
	if err := ledger.Append(h.d.JigHome, journal.Line{
		TS:     ledger.Stamp(h.d.Now()),
		Event:  journal.EventGitCommit,
		Commit: sha,
		Cwd:    cwd,
		Branch: strings.TrimSpace(branch),
	}); err != nil {
		h.fail(err)
		return Result{}
	}
	if h.locate() {
		h.verifyCommit(sha)
	}
	return Result{}
}

// verifyCommit starts the verification of sha for the slice it is for, unless
// one was started or recorded already: a commit is seen by git's hook and by
// the tool call that made it, and verified once. A commit that names no slice
// of the located ticket is not jig to verify.
func (h *handler) verifyCommit(sha string) {
	if h.st == nil || h.w.Ticket == "" || h.w.RepoDir == "" {
		return
	}
	slice, err := h.sliceOf(sha)
	if err != nil {
		h.fail(err)
		return
	}
	if slice == "" || h.alreadySpawned(sha, slice) {
		return
	}
	repo, target := h.repoAndTarget()
	_, err = verify.Spawn(verify.Deps{
		Store:      h.st,
		JigHome:    h.d.JigHome,
		Clone:      h.w.RepoDir,
		Repo:       repo,
		Target:     target,
		Now:        h.d.Now,
		Executable: h.d.Executable,
		Start:      h.d.Start,
	}, verify.Request{Ticket: h.w.Ticket, Slice: slice, SHA: sha, Session: h.session(), Agent: h.ev.Agent})
	h.fail(err)
}
