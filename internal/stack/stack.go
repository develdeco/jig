// Package stack says where the branch of a stacked ticket starts and where its
// pull request goes, in each repo it builds in, and whether the branch of the
// ticket it is stacked on has moved past it. A ticket is stacked on another
// when ticket.yaml names that one as a blocker of kind stacked (ADR 0010): it
// is built on the other's branch before that work has merged.
//
// In a repo the ticket shares with its parent, its branch starts from the head
// of the parent's branch and its pull request goes into the parent's branch,
// until the parent's work merges there; in a repo only the ticket builds in,
// and in a shared repo once the parent merged, both are the repo's target
// branch. What this package says is advice for `jig here`, `jig next`, the
// publish of the pull request and the correction notes: jig never rebases or
// merges a branch, in a clone it does not own.
//
// Read gathers the facts (the store, the clones' refs, whether the parent's
// work merged, which a Merges says) and Judge decides from them, reading
// nothing.
package stack

import (
	"os"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// KindStacked is the blocker kind of a parent a ticket is stacked on.
const KindStacked = "stacked"

// Merges says whether the work of a ticket has merged, in each repo it builds
// in: internal/mergefacts' Reader is one.
type Merges interface {
	Of(ticket string) (ready.Merge, error)
}

// Input is what Read reads from.
type Input struct {
	Store *store.Store
	Cfg   project.Config
	// Clones maps a repo's name to its clone on this machine. A repo with none
	// has no branch heads to read: its start point is named, not resolved.
	Clones map[string]string
	// Merges says whether a parent's work merged; nil reads none, so no parent
	// is taken to have merged.
	Merges Merges
}

// Repo is where the ticket's branch starts in one repo and where its pull
// request goes.
type Repo struct {
	// Repo is the repo's name in the project and Target its target branch.
	Repo, Target string
	// StartFrom is the ref the ticket's branch starts from: the parent's branch
	// as the clone holds it ("jig/JIG-1", or "origin/jig/JIG-1" when only the
	// origin has it), else origin/<target>.
	StartFrom string
	// PRTarget is the branch the ticket's pull request goes into: the parent's
	// branch while it has not merged, else the container's branch, else
	// Target.
	PRTarget string
	// Container is the ticket whose branch the pull request goes into, and
	// ContainerBranch that branch; both "" for a ticket with no container.
	Container, ContainerBranch string
	// ContainerHead is the tip of the container's branch in the clone, "" when
	// it holds no copy; ContainerFrom is the ref to create that branch from.
	ContainerHead, ContainerFrom string
	// Parent is the ticket the branch is stacked on in this repo, "" when the
	// repo is the ticket's alone. ParentBranch is its branch, ParentRef the ref
	// its head was read from, and ParentHead that head, "" when the clone has
	// no copy of the branch.
	Parent, ParentBranch, ParentRef, ParentHead string
	// Merged says the parent's work has merged in this repo: the ticket starts
	// from and goes into Target again.
	Merged bool
	// Head is the tip of the ticket's own branch in the clone, "" while it has
	// none.
	Head string
	// Behind says the parent's branch moved past the ticket's: ParentHead is
	// not in the ticket's branch (their merge base is not the parent's head),
	// so the ticket is to merge it in.
	Behind bool
	// Retarget says the parent merged while the ticket has a branch that does
	// not hold the head of the base (the container's branch, else Target): its
	// pull request is to go into the base and the ticket is to merge it.
	Retarget bool
}

// Base is the branch the ticket's pull request goes into once no parent is
// built on: the container's branch, else Target.
func (r Repo) Base() string {
	if r.ContainerBranch != "" {
		return r.ContainerBranch
	}
	return r.Target
}

// Shared says the repo is one the ticket shares with a parent that has not
// merged there: its branch builds on the parent's.
func (r Repo) Shared() bool { return r.Parent != "" && !r.Merged }

// Plan is where a ticket's branch starts and where its pull request goes, in
// each repo it builds in.
type Plan struct {
	Ticket string
	// Kind is "issue" or "multi-task" for a plan of such a ticket's own branch
	// (the union of its tasks' repos), "" for a task.
	Kind string
	// Branch is the ticket's branch, which has one name in every repo.
	Branch string
	// Repos are the repos the ticket builds in, in the project's order. A
	// ticket stacked on nothing has none: its branch starts from, and its pull
	// request goes into, each repo's target.
	Repos []Repo
}

// HasContainer says the ticket's pull request goes into a container's branch.
func (p Plan) HasContainer() bool {
	for _, r := range p.Repos {
		if r.Container != "" {
			return true
		}
	}
	return false
}

// Stacked says some repo of the ticket is shared with a parent.
func (p Plan) Stacked() bool {
	for _, r := range p.Repos {
		if r.Parent != "" {
			return true
		}
	}
	return false
}

// In is the plan's repo called name.
func (p Plan) In(name string) (Repo, bool) {
	for _, r := range p.Repos {
		if r.Repo == name {
			return r, true
		}
	}
	return Repo{}, false
}

// Parent is what Judge knows of one parent in one repo.
type Parent struct {
	Ticket, Branch string
	// Ref and Head are where the clone holds the parent's branch and its tip;
	// both "" when it holds no copy.
	Ref, Head string
	// Merged says the parent's work merged in the repo.
	Merged bool
	// InHead says Head is in the ticket's branch: false when either is
	// unknown.
	InHead bool
}

// Facts is what Judge decides one repo from.
type Facts struct {
	Repo, Target string
	// Parents are the parents that build in the repo, in ticket.yaml order.
	Parents []Parent
	// Head is the ticket's branch tip in the clone, "" when it has none.
	Head string
	// Container and ContainerBranch are the ticket whose branch the pull
	// request goes into and that branch; "" for a ticket with no container.
	Container, ContainerBranch string
	// ContainerHead is the tip of the container's branch in the clone, "" when
	// it holds no copy; ContainerFrom is the ref that branch is cut from when
	// it is to be created (its own container's branch, else the target).
	ContainerHead, ContainerFrom string
	// MergedDep says a ticket this one waits for, with the kind merged, builds
	// in the repo: with a container, the ticket starts from its branch, where
	// that work merged.
	MergedDep bool
	// FromContainer says the ticket starts from the container's branch
	// whatever it depends on (a multi-task's own branch).
	FromContainer bool
	// TargetHeld says the ticket's branch holds the head of the base (the
	// container's branch, else the target) on the origin (false when either
	// is unknown).
	TargetHeld bool
	// Landed says the ticket's own work has merged in the repo: its branch
	// asks nothing more of anyone, whatever the parent's branch or the target
	// did since. Without it, once the ticket's own pull request merged its
	// branch would never hold the target's head again, and the ticket would
	// read as behind for good.
	Landed bool
}

// Judge decides where the ticket's branch starts in the repo and where its
// pull request goes. The parent it builds on is the first of the repo's
// parents whose work has not merged there; when every one has merged, the
// first, merged. A repo with no parent is the target's. A ticket whose own work
// landed in the repo is told to restack or retarget nothing.
func Judge(f Facts) Repo {
	r := Repo{Repo: f.Repo, Target: f.Target, StartFrom: "origin/" + f.Target, PRTarget: f.Target, Head: f.Head,
		Container: f.Container, ContainerBranch: f.ContainerBranch, ContainerHead: f.ContainerHead, ContainerFrom: f.ContainerFrom}
	base := r.Base()
	r.PRTarget = base
	if f.FromContainer {
		r.StartFrom = "origin/" + base
	}
	if len(f.Parents) == 0 {
		if f.MergedDep {
			r.StartFrom = "origin/" + base
		}
		return r
	}
	p := f.Parents[0]
	for _, c := range f.Parents {
		if !c.Merged {
			p = c
			break
		}
	}
	r.Parent, r.ParentBranch, r.ParentRef, r.ParentHead, r.Merged = p.Ticket, p.Branch, p.Ref, p.Head, p.Merged
	if p.Merged {
		r.StartFrom = "origin/" + base
		r.Retarget = f.Head != "" && !f.TargetHeld && !f.Landed
		return r
	}
	r.PRTarget = p.Branch
	r.StartFrom = p.Ref
	if r.StartFrom == "" {
		r.StartFrom = "origin/" + p.Branch
	}
	r.Behind = f.Head != "" && p.Head != "" && !p.InHead && !f.Landed
	return r
}

// StackedOnAny reports whether rec names a blocker of kind stacked: a ticket
// whose record names none has an empty plan, so a caller that would load the
// project only for Read can skip it.
func StackedOnAny(rec store.Ticket) bool {
	for _, b := range rec.BlockedBy {
		if b.Kind == KindStacked {
			return true
		}
	}
	return false
}

// Read is the plan of ticket. A ticket with no stacked blocker has an empty
// plan, read from its record alone. A parent that is closed is not built on:
// its closure says whether the ticket still waits for it. In a repo of the
// ticket's, a parent counts when its own slices build there too.
func Read(in Input, ticket string) (Plan, error) {
	rec, err := in.Store.ReadTicket(ticket)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Ticket: ticket}
	type parent struct {
		id    string
		rec   store.Ticket
		repos map[string]bool
		merge *ready.Merge
	}
	var parents []parent
	for _, b := range rec.BlockedBy {
		if b.Kind != KindStacked {
			continue
		}
		id := b.Ticket
		if resolved, err := in.Store.ResolveTicket(b.Ticket); err == nil {
			id = resolved
		}
		prec, err := in.Store.ReadTicket(id)
		if err != nil {
			return Plan{}, err
		}
		if prec.Closed != nil {
			continue
		}
		parents = append(parents, parent{id: id, rec: prec})
	}
	container, hasContainer, err := in.Store.ContainerOf(ticket, rec)
	if err != nil {
		return Plan{}, err
	}
	var crec store.Ticket
	if hasContainer {
		if crec, err = in.Store.ReadTicket(container); err != nil {
			return Plan{}, err
		}
	}
	kind := rec.KindOrDefault()
	notTask := kind != store.KindTask
	if len(parents) == 0 && !hasContainer && !notTask {
		return plan, nil
	}
	if notTask {
		plan.Kind = kind
	}

	repos := ready.ProjectRepos(in.Cfg, in.Clones)
	builtIn := func(id string) ([]string, error) {
		slices, err := in.Store.ReadSlices(id)
		if err != nil {
			return nil, err
		}
		return ready.BuiltIn(repos, slices), nil
	}
	var mine []string
	if notTask {
		if mine, err = tasksRepos(in, repos, ticket, kind, container, builtIn); err != nil {
			return Plan{}, err
		}
	} else if mine, err = builtIn(ticket); err != nil {
		return Plan{}, err
	}
	// The tickets this one waits for with the kind merged: with a container,
	// their work merges into its branch, which is then where this one starts.
	mergedDeps := map[string]bool{}
	if hasContainer {
		for _, b := range rec.BlockedBy {
			if b.Kind == KindStacked {
				continue
			}
			id := b.Ticket
			if resolved, err := in.Store.ResolveTicket(id); err == nil {
				id = resolved
			}
			if names, err := builtIn(id); err == nil {
				for _, n := range names {
					mergedDeps[n] = true
				}
			}
		}
	}
	// Whether the ticket's own work has merged is asked once, of the same
	// reader that says whether a parent's did.
	var own *ready.Merge
	if in.Merges != nil && len(parents) > 0 && !notTask {
		m, err := in.Merges.Of(ticket)
		if err != nil {
			return Plan{}, err
		}
		own = &m
	}
	for i := range parents {
		names, err := builtIn(parents[i].id)
		if err != nil {
			return Plan{}, err
		}
		parents[i].repos = map[string]bool{}
		for _, n := range names {
			parents[i].repos[n] = true
		}
		if in.Merges != nil {
			m, err := in.Merges.Of(parents[i].id)
			if err != nil {
				return Plan{}, err
			}
			parents[i].merge = &m
		}
	}

	for _, name := range mine {
		cfgRepo, ok := repoNamed(in.Cfg, name)
		if !ok {
			continue
		}
		target := cfgRepo.TargetBranch()
		branch, err := in.Store.ResolveTicketBranch(ticket, rec, target)
		if err != nil {
			return Plan{}, err
		}
		plan.Branch = branch
		dir := clone(in.Clones[name])
		f := Facts{Repo: name, Target: target, Landed: mergedIn(own, name), MergedDep: mergedDeps[name]}
		base := target
		if hasContainer {
			cb, err := in.Store.ResolveTicketBranch(container, crec, target)
			if err != nil {
				return Plan{}, err
			}
			f.Container, f.ContainerBranch, base = container, cb, cb
			f.FromContainer = kind == store.KindMultiTask
			// The branch the container's own is cut from, were it to be
			// created: its container's (a multi-task's issue), else the target.
			f.ContainerFrom = "origin/" + target
			if crec.KindOrDefault() == store.KindMultiTask {
				if up, ok, err := in.Store.ContainerOf(container, crec); err == nil && ok {
					if urec, err := in.Store.ReadTicket(up); err == nil {
						if ub, err := in.Store.ResolveTicketBranch(up, urec, target); err == nil {
							f.ContainerFrom = "origin/" + ub
						}
					}
				}
			}
			_, f.ContainerHead = tip(dir, cb)
		}
		_, f.Head = tip(dir, branch)
		if f.Head != "" {
			if t := commit(dir, "refs/remotes/origin/"+base); t != "" {
				f.TargetHeld = ancestor(dir, t, f.Head)
			}
		}
		for _, p := range parents {
			if !p.repos[name] {
				continue
			}
			pb, err := in.Store.ResolveTicketBranch(p.id, p.rec, target)
			if err != nil {
				return Plan{}, err
			}
			fp := Parent{Ticket: p.id, Branch: pb, Merged: mergedIn(p.merge, name)}
			fp.Ref, fp.Head = tip(dir, pb)
			if f.Head != "" && fp.Head != "" {
				fp.InHead = ancestor(dir, fp.Head, f.Head)
			}
			f.Parents = append(f.Parents, fp)
		}
		plan.Repos = append(plan.Repos, Judge(f))
	}
	return plan, nil
}

// mergedIn says the merge fact m has the work merged in repo. A ticket that
// builds in one repo has that repo's fact alone; one of several names each.
func mergedIn(m *ready.Merge, repo string) bool {
	if m == nil || !m.Published {
		return false
	}
	if len(m.Repos) == 1 {
		return m.Repos[0].Met
	}
	for _, r := range m.Repos {
		if r.Repo == repo {
			return r.Met
		}
	}
	return false
}

// repoNamed is the project's repo called name.
func repoNamed(cfg project.Config, name string) (project.Repo, bool) {
	for _, r := range cfg.Repos {
		if r.Name() == name {
			return r, true
		}
	}
	return project.Repo{}, false
}

// clone is dir when it is a directory, "" otherwise: a mapping to a clone
// that is not there holds no refs.
func clone(dir string) string {
	if dir == "" {
		return ""
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return ""
	}
	return dir
}

// tip is where the clone holds branch and its head: the local branch, or the
// origin's copy when there is no local one or the origin's is ahead of it. A
// local branch that has diverged from the origin's is the clone's own, and
// wins. Both are "" when the clone holds neither.
func tip(dir, branch string) (ref, head string) {
	if dir == "" {
		return "", ""
	}
	local := commit(dir, "refs/heads/"+branch)
	remote := commit(dir, "refs/remotes/origin/"+branch)
	switch {
	case local == "" && remote == "":
		return "", ""
	case local == "":
		return "origin/" + branch, remote
	case remote == "" || remote == local || ancestor(dir, remote, local):
		return branch, local
	case ancestor(dir, local, remote):
		return "origin/" + branch, remote
	}
	return branch, local
}

// commit is the commit ref names in dir, "" when it names none.
func commit(dir, ref string) string {
	out, found, _ := gitx.ResolveCommit(dir, ref)
	if !found {
		return ""
	}
	return out
}

// tasksRepos is the repos an issue or a multi-task builds in: the union of the
// repos its tasks build in, in the project's order. An issue's tasks are the
// tasks of its chart; a multi-task's are those whose record names it as their
// parent. With none yet it is every repo of the project.
func tasksRepos(in Input, repos []ready.Repo, ticket, kind, container string, builtIn func(string) ([]string, error)) ([]string, error) {
	issue := ticket
	if kind == store.KindMultiTask {
		issue = container
	}
	used := map[string]bool{}
	if issue != "" {
		chart, err := in.Store.ChartOfIssue(issue)
		if err != nil {
			return nil, err
		}
		var entries []store.ChartEntry
		if chart != "" {
			if entries, err = in.Store.ReadChart(chart); err != nil {
				return nil, err
			}
		}
		for _, e := range entries {
			if e.ID == "" {
				continue
			}
			erec, err := in.Store.ReadTicket(e.ID)
			if err != nil || erec.KindOrDefault() != store.KindTask {
				continue
			}
			if kind == store.KindMultiTask && erec.Parent != ticket {
				continue
			}
			names, err := builtIn(e.ID)
			if err != nil {
				continue
			}
			for _, n := range names {
				used[n] = true
			}
		}
	}
	var out []string
	for _, r := range repos {
		if len(used) == 0 || used[r.Name] {
			out = append(out, r.Name)
		}
	}
	return out, nil
}

// ancestor says a is in the history of b; false when git cannot tell.
func ancestor(dir, a, b string) bool {
	ok, err := gitx.IsAncestor(dir, a, b)
	return err == nil && ok
}
