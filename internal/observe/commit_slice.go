package observe

import (
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// sliceTrailer is the commit trailer that names the slice a commit is for.
const sliceTrailer = "Jig-Slice"

// sliceOf is the slice of the located ticket a commit is for: its Jig-Slice
// trailer, else the slice the session has claimed. A name the ticket has no
// slice of, or whose slice builds in another repo than the one the commit is
// in, is passed over.
func (h *handler) sliceOf(sha string) (string, error) {
	slices, err := h.st.ReadSlices(h.w.Ticket)
	if err != nil {
		return "", err
	}
	here := h.buildsHere(slices)
	has := func(id string) bool {
		for _, s := range slices {
			if s.ID == id {
				return here(s)
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

// buildsHere says whether a slice of slices builds in the repo the event is in.
// A project of one repo builds every slice there; in a project of several a
// slice builds in the repo its workspace names, or the one manifest that
// declares it. A slice no repo owns yet (validate says so) is not turned away.
func (h *handler) buildsHere(slices []store.Slice) func(store.Slice) bool {
	if len(h.cfg.Repos) < 2 || h.w.Repo == "" {
		return func(store.Slice) bool { return true }
	}
	clones := map[string]string{}
	if machine, err := project.LoadMachine(h.d.JigHome); err == nil {
		for name, dir := range machine[h.w.Project].Clones {
			clones[name] = dir
		}
	}
	if clones[h.w.Repo] == "" {
		clones[h.w.Repo] = h.w.RepoDir
	}
	place := ready.Place(ready.ProjectRepos(h.cfg, clones), slices)
	return func(s store.Slice) bool {
		repo := place.Repo[s.Workspace]
		return repo == ready.Unplaced || repo == h.w.Repo
	}
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
// branch. A repo the project does not list has no target (a commit with no
// known fork point is not held to one); the project's only repo is the repo
// of a Locate that named none.
func (h *handler) repoAndTarget() (name, target string) {
	for _, r := range h.cfg.Repos {
		if h.w.Repo != "" && r.Name() == h.w.Repo {
			return r.Name(), r.TargetBranch()
		}
	}
	if len(h.cfg.Repos) == 1 {
		return h.cfg.Repos[0].Name(), h.cfg.Repos[0].TargetBranch()
	}
	return h.w.Repo, ""
}
