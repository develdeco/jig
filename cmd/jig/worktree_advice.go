package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/stack"
	"github.com/develdeco/jig/internal/store"
)

// The actions of a nextWorktree row (ADR 0039).
const (
	// worktreeAdd: the ticket's work is wanted in the repo and no work tree
	// holds its branch, so the row gives the git command that adds one.
	worktreeAdd = "add"
	// worktreePresent: the work is wanted and a work tree holds the branch,
	// the clone's own or a linked one.
	worktreePresent = "present"
	// worktreeRemove: the ticket ended and a linked worktree still holds its
	// branch, so the row gives the git command that removes it.
	worktreeRemove = "remove"
	// worktreeNoClone: the work is wanted in a repo that has no clone on this
	// machine.
	worktreeNoClone = "no_clone"
)

// worktreesDirSuffix is what follows a clone's directory in the name of the
// folder that holds the worktrees of its tickets: `<clone>.worktrees/<TICKET>`.
const worktreesDirSuffix = ".worktrees/"

// nextWorktree is one repo's worktree of a ticket as jig advises it. jig adds
// and removes none: Command is what the session runs (ADR 0039).
type nextWorktree struct {
	Repo    string `json:"repo"`
	Action  string `json:"action"`            // add, present, remove or no_clone
	Branch  string `json:"branch,omitempty"`  // the ticket's branch
	Clone   string `json:"clone,omitempty"`   // the repo's clone on this machine
	Dir     string `json:"dir,omitempty"`     // where the worktree goes (add) or is (present, remove)
	Base    string `json:"base,omitempty"`    // add of a branch the clone lacks: its start point
	Command string `json:"command,omitempty"` // the git command to run (add, remove)
	Note    string `json:"note,omitempty"`    // no_clone: what to know
}

// worktreeFacts are what the rows of one ticket are decided from. Every caller
// builds the same struct: `jig next <ticket>` from values it already has, the
// chart and `jig publish --pr` through worktreeSource.factsOf.
type worktreeFacts struct {
	Ticket string
	// Branch is the ticket's branch, which has one name in every repo.
	Branch string
	Slices []store.Slice
	// Plan is the ticket's stack plan: its start point in each repo, and
	// whether a published ticket is owed a merge.
	Plan      stack.Plan
	Closed    bool
	Published bool
	// Wanted says the ticket's work is wanted in a worktree now (see
	// nextTicket.worktreeWanted).
	Wanted bool
}

// worktreeSource decides the worktree rows of tickets of one project on this
// machine. It lists a clone's work trees only when the ticket's branch exists
// in it, and only once for each clone, however many tickets it is asked about.
// The two functions default to git (gitx) and are replaceable so a test can
// decide rows without starting it. An error from either is no worktree.
type worktreeSource struct {
	st     *store.Store
	cfg    project.Config
	mp     project.MachineProject
	merges mergeSource

	listWorktrees func(clone string) ([]gitx.Worktree, error)
	branchExists  func(clone, branch string) bool

	listed map[string][]gitx.Worktree
}

// newWorktreeSource is the source of the project cfg with the clones mp maps,
// reading tickets from st; merges says whether a stacked ticket's parent merged
// (the stack plan needs it).
func newWorktreeSource(st *store.Store, cfg project.Config, mp project.MachineProject, merges mergeSource) *worktreeSource {
	return &worktreeSource{
		st: st, cfg: cfg, mp: mp, merges: merges,
		listWorktrees: gitx.Worktrees,
		branchExists:  localBranchExists,
		listed:        map[string][]gitx.Worktree{},
	}
}

// localBranchExists says branch is a local branch of the clone, read in process.
func localBranchExists(clone, branch string) bool {
	_, ok, err := gitx.ResolveCommit(clone, "refs/heads/"+branch)
	return err == nil && ok
}

// factsOf reads the facts of ticket id from the store: its slices, branch and
// stack plan, and whether it published or was closed. Wanted is left false; the
// caller that wants the ticket's work says so.
func (s *worktreeSource) factsOf(id string) (worktreeFacts, error) {
	data, err := readTicketData(s.st, id)
	if err != nil {
		return worktreeFacts{}, err
	}
	branch, err := ticketBranch(s.st, s.cfg, id, data.rec)
	if err != nil {
		return worktreeFacts{}, err
	}
	plan, err := stack.Read(stackInput(s.st, s.cfg, s.mp, s.merges), id)
	if err != nil {
		return worktreeFacts{}, err
	}
	return worktreeFacts{
		Ticket: id, Branch: branch, Slices: data.in.Slices, Plan: plan,
		Closed: data.rec.Closed != nil, Published: data.in.Published,
	}, nil
}

// worktreeEnded says the ticket's work is over: it was closed, or it published
// and its stack plan owes its branch nothing. A published ticket that is behind
// its parent's branch, or whose parent merged and which has yet to retarget, is
// not over: its merge is made in its worktree.
func worktreeEnded(closed, published bool, plan stack.Plan) bool {
	if closed {
		return true
	}
	if !published {
		return false
	}
	for _, r := range plan.Repos {
		if r.Behind || r.Retarget {
			return false
		}
	}
	return true
}

// worktreeWanted says the ticket's work is wanted in a worktree: its phase is
// known, the ticket has not ended, it has a brief and no blocker holds it. Work
// that has not been briefed or is held by a blocker has no worktree yet.
func (n nextTicket) worktreeWanted() bool {
	t := n.derived
	if t == nil || n.State == ready.StateClosed || n.State == ready.StateDone {
		return false
	}
	return t.Current != phase.Brief && t.Code != phase.CodeBlocked
}

// rows decides the ticket's rows, one for each repo it builds in, in the
// project's order. An ended ticket has a remove row for each linked worktree
// that still holds its branch and nothing else; a ticket whose work is not
// wanted has none; otherwise a repo with no clone here is no_clone, one whose
// branch a work tree holds is present, and any other is an add.
func (s *worktreeSource) rows(f worktreeFacts) []nextWorktree {
	ended := worktreeEnded(f.Closed, f.Published, f.Plan)
	if !ended && !f.Wanted {
		return nil
	}
	var rows []nextWorktree
	for _, repo := range ready.BuiltIn(ready.ProjectRepos(s.cfg, s.mp.Clones), f.Slices) {
		clone, ok := s.cloneOf(repo)
		if !ok {
			if !ended {
				rows = append(rows, nextWorktree{
					Repo: repo, Action: worktreeNoClone, Branch: f.Branch,
					Note: fmt.Sprintf("repo %s has no clone on this machine: %s has no worktree to add there", repo, f.Ticket),
				})
			}
			continue
		}
		exists := s.branchExists(clone, f.Branch)
		held, isHeld := gitx.Worktree{}, false
		if exists {
			held, isHeld = s.holder(clone, f.Branch)
		}
		row := nextWorktree{Repo: repo, Branch: f.Branch, Clone: clone}
		switch {
		case ended && isHeld && !held.Main:
			row.Action, row.Dir = worktreeRemove, filepath.ToSlash(held.Dir)
			row.Command = "git -C " + gitPath(clone) + " worktree remove " + gitPath(row.Dir)
		case ended:
			continue
		case isHeld:
			row.Action, row.Dir = worktreePresent, filepath.ToSlash(held.Dir)
		default:
			row.Action = worktreeAdd
			row.Dir = filepath.ToSlash(filepath.Clean(clone) + worktreesDirSuffix + f.Ticket)
			add := "git -C " + gitPath(clone) + " worktree add " + gitPath(row.Dir)
			if exists {
				row.Command = add + " " + f.Branch
			} else {
				row.Base = startPoint(s.cfg, f.Plan, repo)
				row.Command = add + " -b " + f.Branch
				if row.Base != "" {
					row.Command += " " + row.Base
				}
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// cloneOf is the repo's clone on this machine, with forward slashes: the
// mapped directory when it is one on disk.
func (s *worktreeSource) cloneOf(repo string) (string, bool) {
	dir := s.mp.Clones[repo]
	if dir == "" {
		return "", false
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", false
	}
	return filepath.ToSlash(filepath.Clean(dir)), true
}

// holder is the work tree of the clone that holds branch, by its branch and
// never by its path, so a worktree made by hand anywhere is found. A work tree
// whose directory is gone (prunable) holds nothing. The clone's work trees are
// asked of git once.
func (s *worktreeSource) holder(clone, branch string) (gitx.Worktree, bool) {
	trees, ok := s.listed[clone]
	if !ok {
		var err error
		if trees, err = s.listWorktrees(clone); err != nil {
			trees = nil
		}
		s.listed[clone] = trees
	}
	for _, w := range trees {
		if w.Branch == branch && !w.Prunable {
			return w, true
		}
	}
	return gitx.Worktree{}, false
}

// gitPath writes a path into a git command: wrapped in double quotes when it
// has whitespace.
func gitPath(p string) string {
	if strings.ContainsAny(p, " \t") {
		return `"` + p + `"`
	}
	return p
}

// worktreeCells are the cells of a row of the worktrees table, "-" for an
// empty one.
func worktreeCells(r nextWorktree) []string {
	cells := []string{r.Repo, r.Action, r.Dir, r.Base, r.Command}
	for i, c := range cells {
		if c == "" {
			cells[i] = "-"
		}
	}
	return cells
}

// worktreeBlocks are the text form of a ticket's rows: the worktrees table and,
// after it, one note for each repo with no clone. Nothing for no rows.
func worktreeBlocks(rows []nextWorktree) []string {
	if len(rows) == 0 {
		return nil
	}
	cells := make([][]string, len(rows))
	for i, r := range rows {
		cells[i] = worktreeCells(r)
	}
	blocks := []string{axi.Table("worktrees", []string{"repo", "action", "dir", "base", "command"}, cells)}
	for _, r := range rows {
		if r.Action == worktreeNoClone && r.Note != "" {
			blocks = append(blocks, "note: "+r.Note)
		}
	}
	return blocks
}

// worktreeHelp are the lines that tell a session to run the commands of the
// rows: one for each add and each remove, none for a repo whose worktree is
// there already.
func worktreeHelp(ticket string, rows []nextWorktree) []string {
	var help []string
	for _, r := range rows {
		switch r.Action {
		case worktreeAdd:
			help = append(help, fmt.Sprintf("Add the worktree of %s in %s: `%s`", ticket, r.Repo, r.Command))
		case worktreeRemove:
			help = append(help, fmt.Sprintf("Remove the worktree of %s in %s (git refuses one with changes): `%s`", ticket, r.Repo, r.Command))
		}
	}
	return help
}
