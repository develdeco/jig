package main

import (
	"fmt"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
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
			return stackRows(plan, hereIsPublished(c)), nil
		},
	})
}

// nextStack is one repo of a stacked ticket as `jig next <ticket>` and `jig
// here` name it: where its branch starts (StartFrom) and where its pull
// request goes (PRTarget), the parent it builds on there and the head of the
// parent's branch, and whether the ticket is to merge the parent's branch in
// (Restack) or retarget its pull request because the parent merged
// (Retarget). A row that asks for a merge names it (Merge), and, once the
// ticket published, the publish that pushes the merge and points the open pull
// request at its base (Publish): the merge is the session's, jig never makes it.
type nextStack struct {
	Repo         string `json:"repo"`
	Parent       string `json:"parent,omitempty"`
	ParentBranch string `json:"parent_branch,omitempty"`
	ParentHead   string `json:"parent_head,omitempty"`
	// Container is the ticket whose branch the pull request goes into, and
	// ContainerBranch that branch; Push, when the clone holds no copy of the
	// branch, the command that creates it on the origin from where it is cut.
	Container       string `json:"container,omitempty"`
	ContainerBranch string `json:"container_branch,omitempty"`
	Push            string `json:"push,omitempty"`
	Merged          bool   `json:"merged,omitempty"`
	StartFrom       string `json:"start_from"`
	PRTarget        string `json:"pr_target"`
	Restack         bool   `json:"restack,omitempty"`
	Retarget        bool   `json:"retarget,omitempty"`
	Merge           string `json:"merge,omitempty"`
	Publish         string `json:"publish,omitempty"`
}

// stackRows are the repos of plan as nextStack rows; none when the ticket is
// stacked on nothing. published says the ticket published: a row that asks for
// a merge then names the publish that follows it, `jig publish <ticket>` (with
// --repo when the ticket has several rows, since the plan, the lookup of the
// pull request and the update are each repo's own).
func stackRows(plan stack.Plan, published bool) []nextStack {
	if !plan.Stacked() && !plan.HasContainer() && plan.Kind == "" {
		return nil
	}
	rows := make([]nextStack, 0, len(plan.Repos))
	for _, r := range plan.Repos {
		row := nextStack{
			Repo: r.Repo, Parent: r.Parent, ParentBranch: r.ParentBranch, ParentHead: r.ParentHead, Merged: r.Merged,
			Container: r.Container, ContainerBranch: r.ContainerBranch,
			StartFrom: r.StartFrom, PRTarget: r.PRTarget, Restack: r.Behind, Retarget: r.Retarget,
		}
		if r.Container != "" && r.ContainerHead == "" && r.ContainerFrom != "" {
			row.Push = "git push origin " + r.ContainerFrom + ":refs/heads/" + r.ContainerBranch
		}
		if r.Behind || r.Retarget {
			row.Merge = "git merge " + r.StartFrom
			if published {
				row.Publish = "jig publish " + plan.Ticket
				if len(plan.Repos) > 1 {
					row.Publish += " --repo " + r.Repo
				}
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// stackBlocks renders the rows of ticket: a table of each repo's start point
// and pull request target, then one note for each repo whose parent's branch
// moved past the ticket's, whose parent merged, or whose parent's branch the
// clone has no copy of. A restack or retarget note of a published ticket ends
// with the publish that follows the merge.
func stackBlocks(ticket string, rows []nextStack) []string {
	if len(rows) == 0 {
		return nil
	}
	table := make([][]string, len(rows))
	for i, r := range rows {
		parent := r.Parent
		switch {
		case parent == "" && r.Container != "":
			parent = r.Container
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
				r.Parent, ticket, r.Repo, r.ParentBranch, shortSHA(r.ParentHead), r.StartFrom)+thenPublish("; ", r.Publish))
		case r.Retarget:
			// jig publish points the open PR at its target (ADR 0038), but only
			// the full publish does: the hand-off ends in recording a pull
			// request somebody opened, which leaves its base alone, so the note
			// gives the edit for that path too.
			target := r.PRTarget
			note := fmt.Sprintf("note: %s merged in %s: merge %s (`git merge origin/%s`)", r.Parent, r.Repo, target, target)
			if r.Publish != "" {
				note += fmt.Sprintf(", then run `%s`, which points the PR of %s at %s", r.Publish, ticket, target)
			} else {
				note += fmt.Sprintf("; the publish of %s points its PR at %s", ticket, target)
			}
			note += fmt.Sprintf(", but the hand-off (`--body`, then `--pr`) does not: run `gh pr edit <pr> --base %s`", target)
			blocks = append(blocks, note)
		case r.Parent != "" && !r.Merged && r.ParentHead == "":
			blocks = append(blocks, fmt.Sprintf("note: the clone of %s holds no copy of %s, the branch of %s: fetch it (`git fetch origin`) before %s starts from it",
				r.Repo, r.ParentBranch, r.Parent, ticket))
		case r.Push != "":
			blocks = append(blocks, fmt.Sprintf("note: the clone of %s holds no copy of %s, the branch of %s: fetch it (`git fetch origin`); if the origin has none either, create it there from its own base: `%s`",
				r.Repo, r.ContainerBranch, r.Container, r.Push))
		}
	}
	return blocks
}

// thenPublish ends a note with the publish that follows its merge: the empty
// string when the row names none.
func thenPublish(sep, publish string) string {
	if publish == "" {
		return ""
	}
	return sep + "then run `" + publish + "`"
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

// notedStack is the stack plan the correction notes read at a tool call, a
// prompt and a stop. Only a ticket whose record names a stacked blocker can be
// told to restack or retarget, so any other is told from that record alone:
// no chart file, no tracker record and no other ticket is read for it. The
// plan of one that does reads no ticket it does not name
// (mergefacts.Reader.NamedOnly). `jig here` keeps the full plan, container
// rows included (locatedStack).
func notedStack(where locate.Where, st *store.Store, jigHome string) (stack.Plan, error) {
	rec, err := st.ReadTicket(where.Ticket)
	if err != nil {
		return stack.Plan{}, err
	}
	if !stack.StackedOnAny(rec) {
		return stack.Plan{Ticket: where.Ticket}, nil
	}
	return locatedStack(nil, where, st, jigHome, true)
}

// locatedStack reads the stack plan of the ticket where names, in the store st
// it resolved: the project's repos from the store and the marker, its clones
// from the machine mapping, and whether a parent merged from what this
// machine knows (cachedMergeReader). A ticket with no stack facts
// (hasStackFacts) has an empty plan. namedOnly is
// mergefacts.Reader.NamedOnly, for a plan a tool call reads.
func locatedStack(m *gitx.Memo, where locate.Where, st *store.Store, jigHome string, namedOnly bool) (stack.Plan, error) {
	rec, err := st.ReadTicket(where.Ticket)
	if err != nil {
		return stack.Plan{}, err
	}
	if !hasStackFacts(st, where.Ticket, rec) {
		return stack.Plan{Ticket: where.Ticket}, nil
	}
	cfg, err := project.LoadConfigWith(m, where.JigHome, where.StorePath, where.Project, where.Root, where.RepoDir)
	if err != nil {
		return stack.Plan{}, err
	}
	machine, err := project.LoadMachine(jigHome)
	if err != nil {
		return stack.Plan{}, err
	}
	mp := machine[cfg.Name]
	merges := cachedMergeReader(st, cfg, mp, jigHome)
	merges.NamedOnly = namedOnly
	return stack.Read(stackInput(st, cfg, mp, merges), where.Ticket)
}

// hasStackFacts says the task's stack plan can say something: its record names
// a stacked blocker, or it has a container (a parent, or the one chart with an
// issue that lists it). The record is checked first, so a ticket outside every
// chart costs one pass over the charts' files and no more. An issue and a
// multi-task have none here: `jig next` reads their own branch.
func hasStackFacts(st *store.Store, id string, rec store.Ticket) bool {
	if rec.KindOrDefault() != store.KindTask {
		return false
	}
	if stack.StackedOnAny(rec) || rec.Parent != "" {
		return true
	}
	_, ok, err := st.ContainerOf(id, rec)
	return err == nil && ok
}

// hereStack is the stack plan of the located ticket; ok is false when `jig
// here` located no store or no ticket, or the ticket is stacked on nothing. A
// plan that cannot be read is no plan here: `jig next <ticket>` says why.
func hereStack(c hereCtx) (stack.Plan, bool) {
	if c.Store == nil || c.Where.Ticket == "" {
		return stack.Plan{}, false
	}
	plan, err := locatedStack(c.e.memo, c.Where, c.Store, c.JigHome, false)
	if err != nil || (!plan.Stacked() && !plan.HasContainer()) {
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
	return stackBlocks(plan.Ticket, stackRows(plan, hereIsPublished(c))), nil
}

// hereIsPublished says the located ticket published, as its journal records
// it; a journal that cannot be read says no, and the rows name no publish.
func hereIsPublished(c hereCtx) bool {
	published, err := isPublished(c.Store, c.Where.Ticket)
	return err == nil && published
}
