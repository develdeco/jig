package main

import (
	"fmt"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/mergefacts"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/stack"
	"github.com/develdeco/jig/internal/store"
)

// init registers `jig here`'s stack section (sections.go): for a ticket
// stacked on another, where its branch starts and where its pull request goes
// in each repo it builds in, and what the parent's branch asks of it, so a
// session that starts on the ticket knows which branch it builds on before it
// cuts its own.
func init() {
	registerHereSection(hereSection{
		Order:  310,
		Name:   "stack",
		Blocks: hereStackBlocks,
		JSON: func(c hereCtx) (any, error) {
			plan, ok := hereStack(c)
			if !ok {
				return nil, nil
			}
			return stackRows(plan), nil
		},
	})
}

// nextStack is one repo of a stacked ticket as `jig next <ticket>` and `jig
// here` name it: where its branch starts (StartFrom) and where its pull
// request goes (PRTarget), the parent it builds on there and the head of the
// parent's branch, and whether the ticket is to merge the parent's branch in
// (Restack) or retarget its pull request because the parent merged
// (Retarget).
type nextStack struct {
	Repo         string `json:"repo"`
	Parent       string `json:"parent,omitempty"`
	ParentBranch string `json:"parent_branch,omitempty"`
	ParentHead   string `json:"parent_head,omitempty"`
	Merged       bool   `json:"merged,omitempty"`
	StartFrom    string `json:"start_from"`
	PRTarget     string `json:"pr_target"`
	Restack      bool   `json:"restack,omitempty"`
	Retarget     bool   `json:"retarget,omitempty"`
}

// stackRows are the repos of plan as nextStack rows; none when the ticket is
// stacked on nothing.
func stackRows(plan stack.Plan) []nextStack {
	if !plan.Stacked() {
		return nil
	}
	rows := make([]nextStack, 0, len(plan.Repos))
	for _, r := range plan.Repos {
		rows = append(rows, nextStack{
			Repo: r.Repo, Parent: r.Parent, ParentBranch: r.ParentBranch, ParentHead: r.ParentHead, Merged: r.Merged,
			StartFrom: r.StartFrom, PRTarget: r.PRTarget, Restack: r.Behind, Retarget: r.Retarget,
		})
	}
	return rows
}

// stackBlocks renders the rows of ticket: a table of each repo's start point
// and pull request target, then one note for each repo whose parent's branch
// moved past the ticket's, whose parent merged, or whose parent's branch the
// clone has no copy of.
func stackBlocks(ticket string, rows []nextStack) []string {
	if len(rows) == 0 {
		return nil
	}
	table := make([][]string, len(rows))
	for i, r := range rows {
		parent := r.Parent
		switch {
		case parent == "":
			parent = "-"
		case r.Merged:
			parent += " (merged)"
		}
		table[i] = []string{r.Repo, parent, r.StartFrom, r.PRTarget}
	}
	blocks := []string{axi.Table("stack", []string{"repo", "parent", "start_from", "pr_target"}, table)}
	for _, r := range rows {
		switch {
		case r.Restack:
			blocks = append(blocks, fmt.Sprintf("note: the branch of %s moved past %s in %s: restack onto `%s` at `%s`: `git merge %s`; jig never rebases",
				r.Parent, ticket, r.Repo, r.ParentBranch, shortSHA(r.ParentHead), r.StartFrom))
		case r.Retarget:
			target := r.PRTarget
			blocks = append(blocks, fmt.Sprintf("note: %s merged in %s: retarget the PR of %s to %s and merge %s (`git merge origin/%s`)",
				r.Parent, r.Repo, ticket, target, target, target))
		case r.Parent != "" && !r.Merged && r.ParentHead == "":
			blocks = append(blocks, fmt.Sprintf("note: the clone of %s holds no copy of %s, the branch of %s: fetch it (`git fetch origin`) before %s starts from it",
				r.Repo, r.ParentBranch, r.Parent, ticket))
		}
	}
	return blocks
}

// stackInput is what a stack plan of the project cfg is read from, with merges
// saying whether a parent's work merged.
func stackInput(st *store.Store, cfg project.Config, mp project.MachineProject, merges stack.Merges) stack.Input {
	return stack.Input{Store: st, Cfg: cfg, Clones: mp.Clones, Merges: merges}
}

// cachedMergeReader is a merge reader that asks no host: it takes what this
// machine kept of each pull request's state, and the branches in the clones.
// It is for what runs at every session start and tool call (`jig here`, the
// correction notes), which must not wait on the network.
func cachedMergeReader(st *store.Store, cfg project.Config, mp project.MachineProject, jigHome string) *mergefacts.Reader {
	cache := &mergefacts.PRCache{}
	if jigHome != "" {
		cache.Path = home.PullRequestStatesPath(jigHome)
	}
	return &mergefacts.Reader{Store: st, Cfg: cfg, Clones: mp.Clones, PRs: cache}
}

// locatedStack reads the stack plan of the ticket where names, in the store st
// it resolved: the project's repos from the store and the marker, its clones
// from the machine mapping, and whether a parent merged from what this
// machine knows (cachedMergeReader). A ticket stacked on nothing is told from
// its record alone: the hook asks at every event.
func locatedStack(where locate.Where, st *store.Store, jigHome string) (stack.Plan, error) {
	rec, err := st.ReadTicket(where.Ticket)
	if err != nil {
		return stack.Plan{}, err
	}
	if !stack.StackedOnAny(rec) {
		return stack.Plan{Ticket: where.Ticket}, nil
	}
	cfg, err := project.LoadConfig(where.JigHome, where.StorePath, where.Project, where.Root, where.RepoDir)
	if err != nil {
		return stack.Plan{}, err
	}
	machine, err := project.LoadMachine(jigHome)
	if err != nil {
		return stack.Plan{}, err
	}
	mp := machine[cfg.Name]
	return stack.Read(stackInput(st, cfg, mp, cachedMergeReader(st, cfg, mp, jigHome)), where.Ticket)
}

// hereStack is the stack plan of the located ticket; ok is false when `jig
// here` located no store or no ticket, or the ticket is stacked on nothing. A
// plan that cannot be read is no plan here: `jig next <ticket>` says why.
func hereStack(c hereCtx) (stack.Plan, bool) {
	if c.Store == nil || c.Where.Ticket == "" {
		return stack.Plan{}, false
	}
	plan, err := locatedStack(c.Where, c.Store, c.JigHome)
	if err != nil || !plan.Stacked() {
		return stack.Plan{}, false
	}
	return plan, true
}

// hereStackBlocks renders the section.
func hereStackBlocks(c hereCtx) ([]string, error) {
	plan, ok := hereStack(c)
	if !ok {
		return nil, nil
	}
	return stackBlocks(plan.Ticket, stackRows(plan)), nil
}
