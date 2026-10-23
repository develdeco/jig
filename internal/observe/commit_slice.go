package observe

import (
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// sliceTrailer is the commit trailer that names the slice a commit is for.
const sliceTrailer = "Jig-Slice"

// sliceOf is the slice of the located ticket a commit is for: its Jig-Slice
// trailer, else the slice the session has claimed. A name the ticket has no
// slice of is passed over.
func (h *handler) sliceOf(sha string) (string, error) {
	slices, err := h.st.ReadSlices(h.w.Ticket)
	if err != nil {
		return "", err
	}
	has := func(id string) bool {
		for _, s := range slices {
			if s.ID == id {
				return true
			}
		}
		return false
	}
	if t, err := gitx.Trailer(h.w.RepoDir, sha, sliceTrailer); err == nil && has(t) {
		return t, nil
	}
	if has(h.w.Slice) {
		return h.w.Slice, nil
	}
	return "", nil
}

// spawnWindow is how far back the ledger is searched for the verification a
// commit already has.
const spawnWindow = 1 << 20

// alreadySpawned reports whether the ledger says a worker was started for the
// commit and slice. The journal says so too, but only once the worker has
// written its first line: this closes the gap in which git's hook has started
// the worker and the tool call that made the commit looks.
func (h *handler) alreadySpawned(sha, slice string) bool {
	found := false
	h.fail(ledger.ReadBackward(h.d.JigHome, spawnWindow, func(l journal.Line) bool {
		if l.Event == journal.EventVerifySpawned && l.Commit == sha && l.Slice == slice {
			found = true
		}
		return !found
	}))
	return found
}

// repoAndTarget is the located repo name in the project and its target
// branch, the first repo of the project when Locate named none.
func (h *handler) repoAndTarget() (name, target string) {
	for _, r := range h.cfg.Repos {
		if h.w.Repo != "" && r.Name() == h.w.Repo {
			return r.Name(), r.TargetBranch()
		}
	}
	if len(h.cfg.Repos) > 0 {
		return h.cfg.Repos[0].Name(), h.cfg.Repos[0].TargetBranch()
	}
	return h.w.Repo, ""
}
