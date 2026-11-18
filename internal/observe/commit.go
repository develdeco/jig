package observe

import (
	"errors"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// postCommit records the commit git just made in the ledger and starts
// nothing: a builder proves its slice by running `jig verify` itself, so a
// commit is only seen here. Its order is the order of the runner it replaced:
// HEAD, then the branch, then the ledger line, so a directory that is no
// repository fails at HEAD.
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
	}
	return Result{}
}
