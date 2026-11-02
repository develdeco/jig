package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/stack"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// The tests here decide the rows of a ticket's worktrees (worktree_advice.go)
// with the two questions put to git replaced by fakes, so no test starts git:
// the repos' clones are plain directories, and what the lister says is what the
// clone's work trees are. The real lister is internal/gitx's (TestWorktrees),
// and the whole chain on the real binary is e2e's TestNextAdvisesAWorktreePerRepo.

// wtRig is a worktreeSource over a project of repos, each with a clone
// directory under one root, and the answers its fakes give.
type wtRig struct {
	src    *worktreeSource
	root   string
	clones map[string]string

	// branches maps a clone to the branches it has; trees maps a clone to the
	// work trees git would list; listErr fails every listing.
	branches map[string][]string
	trees    map[string][]gitx.Worktree
	listErr  error
	listed   []string
}

const wtTicket = "JIG-7"
const wtBranch = "7-the-ticket"

// newWtRig makes a source over repos, each with a clone directory.
func newWtRig(t *testing.T, repos ...string) *wtRig {
	t.Helper()
	r := &wtRig{root: filepath.ToSlash(t.TempDir()), clones: map[string]string{}, branches: map[string][]string{}, trees: map[string][]gitx.Worktree{}}
	var cfg project.Config
	for _, name := range repos {
		cfg.Repos = append(cfg.Repos, project.Repo{Remote: "https://example.invalid/acme/" + name + ".git"})
		dir := r.root + "/" + name
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		r.clones[name] = dir
	}
	r.src = newWorktreeSource(nil, cfg, project.MachineProject{Clones: r.clones}, nil)
	r.src.branchExists = func(clone, branch string) bool {
		for _, b := range r.branches[clone] {
			if b == branch {
				return true
			}
		}
		return false
	}
	r.src.listWorktrees = func(clone string) ([]gitx.Worktree, error) {
		r.listed = append(r.listed, clone)
		return r.trees[clone], r.listErr
	}
	return r
}

// facts are the facts of a wanted, unpublished ticket that builds in every repo.
func (r *wtRig) facts() worktreeFacts {
	return worktreeFacts{Ticket: wtTicket, Branch: wtBranch, Wanted: true}
}

// holds records that clone has the ticket's branch and that the work tree w
// holds it.
func (r *wtRig) holds(repo string, w gitx.Worktree) {
	clone := r.clones[repo]
	r.branches[clone] = append(r.branches[clone], wtBranch)
	w.Branch = wtBranch
	r.trees[clone] = append(r.trees[clone], gitx.Worktree{Dir: clone, Branch: "main", Main: true}, w)
}

func rowOfRepo(t *testing.T, rows []nextWorktree, repo string) nextWorktree {
	t.Helper()
	for _, row := range rows {
		if row.Repo == repo {
			return row
		}
	}
	t.Fatalf("no row for %s in %+v", repo, rows)
	return nextWorktree{}
}

// TestWorktreeRowsAddABranchTheCloneLacks: a ticket whose work is wanted and
// whose branch the clone does not have gets the add that cuts the branch from
// its start point, beside the clone, and git is not asked what the clone's work
// trees are.
func TestWorktreeRowsAddABranchTheCloneLacks(t *testing.T) {
	t.Parallel()
	r := newWtRig(t, "api")
	rows := r.src.rows(r.facts())
	clone := r.clones["api"]
	want := []nextWorktree{{
		Repo: "api", Action: "add", Branch: wtBranch, Clone: clone,
		Dir: clone + ".worktrees/" + wtTicket, Base: "origin/main",
		Command: "git -C " + clone + " worktree add " + clone + ".worktrees/" + wtTicket + " -b " + wtBranch + " origin/main",
	}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}
	if len(r.listed) != 0 {
		t.Errorf("git listed the work trees of %v for a branch the clone lacks", r.listed)
	}
}

// TestWorktreeRowsAddABranchTheCloneHas: a branch that outlived its worktree
// (or was cut by hand) is added without -b and without a base.
func TestWorktreeRowsAddABranchTheCloneHas(t *testing.T) {
	t.Parallel()
	r := newWtRig(t, "api")
	clone := r.clones["api"]
	r.branches[clone] = []string{"main", wtBranch}
	r.trees[clone] = []gitx.Worktree{{Dir: clone, Branch: "main", Main: true}}
	rows := r.src.rows(r.facts())
	want := []nextWorktree{{
		Repo: "api", Action: "add", Branch: wtBranch, Clone: clone,
		Dir:     clone + ".worktrees/" + wtTicket,
		Command: "git -C " + clone + " worktree add " + clone + ".worktrees/" + wtTicket + " " + wtBranch,
	}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}
}

// TestWorktreeRowsPresentInTheClone: the clone's own work tree is on the
// branch, so it is the ticket's worktree and no add is advised that git would
// refuse.
func TestWorktreeRowsPresentInTheClone(t *testing.T) {
	t.Parallel()
	r := newWtRig(t, "api")
	clone := r.clones["api"]
	r.branches[clone] = []string{wtBranch}
	r.trees[clone] = []gitx.Worktree{{Dir: clone, Branch: wtBranch, Main: true}}
	rows := r.src.rows(r.facts())
	want := []nextWorktree{{Repo: "api", Action: "present", Branch: wtBranch, Clone: clone, Dir: clone}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}
}

// TestWorktreeRowsPresentInALinkedWorktree: a worktree made by hand anywhere is
// found by the branch it holds, never by its path.
func TestWorktreeRowsPresentInALinkedWorktree(t *testing.T) {
	t.Parallel()
	r := newWtRig(t, "api")
	r.holds("api", gitx.Worktree{Dir: "/elsewhere/by-hand"})
	rows := r.src.rows(r.facts())
	want := []nextWorktree{{Repo: "api", Action: "present", Branch: wtBranch, Clone: r.clones["api"], Dir: "/elsewhere/by-hand"}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}
}

// TestWorktreeRowsAPrunableEntryHoldsNothing: a worktree whose directory is
// gone does not hold the branch, so the work is advised an add; and a lister
// that fails, or a branch the clone does not have, is no worktree either.
func TestWorktreeRowsAPrunableEntryHoldsNothing(t *testing.T) {
	t.Parallel()
	t.Run("prunable", func(t *testing.T) {
		t.Parallel()
		r := newWtRig(t, "api")
		r.holds("api", gitx.Worktree{Dir: "/gone/wt", Prunable: true})
		if rows := r.src.rows(r.facts()); len(rows) != 1 || rows[0].Action != "add" || strings.Contains(rows[0].Command, " -b ") || rows[0].Base != "" {
			t.Errorf("rows = %+v, want one add of the existing branch", rows)
		}
	})
	t.Run("the listing fails", func(t *testing.T) {
		t.Parallel()
		r := newWtRig(t, "api")
		r.holds("api", gitx.Worktree{Dir: "/by/hand"})
		r.listErr = errors.New("git is not here")
		if rows := r.src.rows(r.facts()); len(rows) != 1 || rows[0].Action != "add" {
			t.Errorf("rows = %+v, want an add when git cannot list", rows)
		}
	})
}

// TestWorktreeRowsStackedStartsFromTheParentsBranch: the base is the start point
// of the ticket's plan in the repo, the parent's branch where they share one and
// the target elsewhere.
func TestWorktreeRowsStackedStartsFromTheParentsBranch(t *testing.T) {
	t.Parallel()
	r := newWtRig(t, "api", "web")
	f := r.facts()
	f.Plan = stack.Plan{Ticket: wtTicket, Repos: []stack.Repo{
		{Repo: "api", Target: "main", StartFrom: "6-the-parent", PRTarget: "6-the-parent", Parent: "JIG-6", ParentBranch: "6-the-parent"},
		{Repo: "web", Target: "main", StartFrom: "origin/main", PRTarget: "main"},
	}}
	rows := r.src.rows(f)
	if got := rowOfRepo(t, rows, "api"); got.Base != "6-the-parent" || !strings.HasSuffix(got.Command, " -b "+wtBranch+" 6-the-parent") {
		t.Errorf("api = %+v, want it to start from the parent's branch", got)
	}
	if got := rowOfRepo(t, rows, "web"); got.Base != "origin/main" || !strings.HasSuffix(got.Command, " -b "+wtBranch+" origin/main") {
		t.Errorf("web = %+v, want it to start from the target", got)
	}
}

// TestWorktreeRowsTwoRepos: a ticket that builds in two repos gets a row for
// each, in the project's order, each beside its own clone so no folder is
// shared, and git is asked once for each clone however many tickets are decided.
func TestWorktreeRowsTwoRepos(t *testing.T) {
	t.Parallel()
	r := newWtRig(t, "api", "web")
	r.holds("web", gitx.Worktree{Dir: r.clones["web"] + ".worktrees/" + wtTicket})
	rows := r.src.rows(r.facts())
	if len(rows) != 2 || rows[0].Repo != "api" || rows[1].Repo != "web" {
		t.Fatalf("rows = %+v, want api then web", rows)
	}
	if rows[0].Action != "add" || rows[0].Dir != r.clones["api"]+".worktrees/"+wtTicket {
		t.Errorf("api = %+v, want an add beside the api clone", rows[0])
	}
	if rows[1].Action != "present" || rows[1].Dir != r.clones["web"]+".worktrees/"+wtTicket {
		t.Errorf("web = %+v, want present at the web clone's worktree", rows[1])
	}
	if rows[0].Dir == rows[1].Dir {
		t.Errorf("two repos share the folder %s", rows[0].Dir)
	}

	// Another ticket of the same source: the clone's work trees are kept.
	other := r.facts()
	other.Ticket = "JIG-8"
	other.Branch = "8-other"
	r.src.rows(other)
	r.src.rows(r.facts())
	if want := []string{r.clones["web"]}; !reflect.DeepEqual(r.listed, want) {
		t.Errorf("git listed %v, want the web clone once (the api clone lacks both branches)", r.listed)
	}
}

// TestWorktreeRowsOnlyTheReposTheTicketBuildsIn: a repo none of the ticket's
// slices is placed in has no row when another is.
func TestWorktreeRowsOnlyTheReposTheTicketBuildsIn(t *testing.T) {
	t.Parallel()
	r := newWtRig(t, "api", "web")
	f := r.facts()
	f.Slices = []store.Slice{{ID: "a", Workspace: "web/ui"}}
	rows := r.src.rows(f)
	if len(rows) != 1 || rows[0].Repo != "web" {
		t.Errorf("rows = %+v, want web alone", rows)
	}
}

// TestWorktreeRowsNoClone: a repo with no clone mapped on this machine, or whose
// clone is no longer on disk, says so and gets no command and no directory; the
// repos with a clone still get theirs.
func TestWorktreeRowsNoClone(t *testing.T) {
	t.Parallel()
	r := newWtRig(t, "api", "web", "docs")
	delete(r.src.mp.Clones, "web")
	if err := os.RemoveAll(r.clones["docs"]); err != nil {
		t.Fatal(err)
	}
	rows := r.src.rows(r.facts())
	if len(rows) != 3 {
		t.Fatalf("rows = %+v, want one for each repo", rows)
	}
	if got := rowOfRepo(t, rows, "api"); got.Action != "add" {
		t.Errorf("api = %+v, want an add", got)
	}
	for _, repo := range []string{"web", "docs"} {
		want := nextWorktree{
			Repo: repo, Action: "no_clone", Branch: wtBranch,
			Note: "repo " + repo + " has no clone on this machine: " + wtTicket + " has no worktree to add there",
		}
		if got := rowOfRepo(t, rows, repo); got != want {
			t.Errorf("%s = %+v, want %+v", repo, got, want)
		}
	}
}

// TestWorktreeRowsPathWithSpaces: a path with whitespace is wrapped in double
// quotes in the command, and written with forward slashes everywhere.
func TestWorktreeRowsPathWithSpaces(t *testing.T) {
	t.Parallel()
	r := newWtRig(t, "api")
	spaced := r.root + "/my work/api"
	if err := os.MkdirAll(spaced, 0o755); err != nil {
		t.Fatal(err)
	}
	r.src.mp.Clones["api"] = filepath.FromSlash(spaced)
	rows := r.src.rows(r.facts())
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	dir := spaced + ".worktrees/" + wtTicket
	want := `git -C "` + spaced + `" worktree add "` + dir + `" -b ` + wtBranch + ` origin/main`
	if rows[0].Command != want || rows[0].Dir != dir || rows[0].Clone != spaced {
		t.Errorf("row = %+v, want dir %s and command %s", rows[0], dir, want)
	}
}

// TestWorktreeRowsNotWanted: work that is not wanted yet (not briefed, or held
// by a blocker) has no rows, and neither has a repo with no clone.
func TestWorktreeRowsNotWanted(t *testing.T) {
	t.Parallel()
	r := newWtRig(t, "api")
	f := r.facts()
	f.Wanted = false
	if rows := r.src.rows(f); len(rows) != 0 {
		t.Errorf("rows = %+v, want none while the work is not wanted", rows)
	}
	if len(r.listed) != 0 {
		t.Errorf("git listed %v for a ticket with no rows", r.listed)
	}
}

// TestWorktreeRowsRemoveOnlyALinkedWorktree: once the ticket published or was
// closed, a linked worktree on its branch is advised a remove (never forced),
// the clone's own work tree is left alone, and a ticket with no worktree, or a
// repo with no clone, has nothing.
func TestWorktreeRowsRemoveOnlyALinkedWorktree(t *testing.T) {
	t.Parallel()
	for _, ended := range []struct {
		name  string
		facts func(worktreeFacts) worktreeFacts
	}{
		{"published", func(f worktreeFacts) worktreeFacts { f.Published = true; return f }},
		{"closed", func(f worktreeFacts) worktreeFacts { f.Closed = true; return f }},
		{"published, wanted by mistake", func(f worktreeFacts) worktreeFacts { f.Published = true; f.Wanted = true; return f }},
	} {
		t.Run(ended.name, func(t *testing.T) {
			t.Parallel()
			r := newWtRig(t, "api", "web", "docs")
			dir := r.clones["api"] + ".worktrees/" + wtTicket
			r.holds("api", gitx.Worktree{Dir: dir})
			r.trees[r.clones["web"]] = []gitx.Worktree{{Dir: r.clones["web"], Branch: wtBranch, Main: true}}
			r.branches[r.clones["web"]] = []string{wtBranch}
			delete(r.src.mp.Clones, "docs")
			rows := r.src.rows(ended.facts(r.facts()))
			want := []nextWorktree{{
				Repo: "api", Action: "remove", Branch: wtBranch, Clone: r.clones["api"], Dir: dir,
				Command: "git -C " + r.clones["api"] + " worktree remove " + dir,
			}}
			if !reflect.DeepEqual(rows, want) {
				t.Errorf("rows = %+v, want only the linked worktree's remove", rows)
			}
			if strings.Contains(rows[0].Command, "--force") {
				t.Errorf("the remove is forced: %s", rows[0].Command)
			}
		})
	}
}

// TestWorktreeRowsAStackedTicketOwedAMergeKeepsItsWorktree: a published ticket
// whose plan owes its branch a restack or a retarget has not ended: it is
// advised a worktree to make the merge in (present while one is there, an add
// of the existing branch once it was removed), and no remove.
func TestWorktreeRowsAStackedTicketOwedAMergeKeepsItsWorktree(t *testing.T) {
	t.Parallel()
	for _, owed := range []struct {
		name string
		repo stack.Repo
	}{
		{"behind its parent", stack.Repo{Repo: "api", Parent: "JIG-6", Behind: true}},
		{"its parent merged", stack.Repo{Repo: "api", Parent: "JIG-6", Merged: true, Retarget: true}},
	} {
		t.Run(owed.name, func(t *testing.T) {
			t.Parallel()
			facts := func(r *wtRig) worktreeFacts {
				f := r.facts()
				f.Published, f.Wanted = true, true
				f.Plan = stack.Plan{Ticket: wtTicket, Repos: []stack.Repo{owed.repo}}
				return f
			}
			with := newWtRig(t, "api")
			with.holds("api", gitx.Worktree{Dir: "/wt/linked"})
			if rows := with.src.rows(facts(with)); len(rows) != 1 || rows[0].Action != "present" || rows[0].Dir != "/wt/linked" {
				t.Errorf("with its worktree: rows = %+v, want present", rows)
			}
			// The worktree was removed after the publish; the branch stayed.
			without := newWtRig(t, "api")
			clone := without.clones["api"]
			without.branches[clone] = []string{wtBranch}
			without.trees[clone] = []gitx.Worktree{{Dir: clone, Branch: "main", Main: true}}
			rows := without.src.rows(facts(without))
			if len(rows) != 1 || rows[0].Action != "add" || strings.Contains(rows[0].Command, " -b ") {
				t.Errorf("without it: rows = %+v, want an add of the branch the clone has", rows)
			}
		})
	}
}

// TestWorktreeEnded: a ticket has ended when it was closed, or published with
// nothing owed its branch; a published ticket that is behind its parent or must
// retarget has not.
func TestWorktreeEnded(t *testing.T) {
	t.Parallel()
	plan := func(r stack.Repo) stack.Plan { return stack.Plan{Repos: []stack.Repo{r}} }
	cases := []struct {
		name              string
		closed, published bool
		plan              stack.Plan
		want              bool
	}{
		{"in flight", false, false, stack.Plan{}, false},
		{"in flight and stacked", false, false, plan(stack.Repo{Parent: "P"}), false},
		{"published", false, true, stack.Plan{}, true},
		{"published and stacked, level", false, true, plan(stack.Repo{Parent: "P"}), true},
		{"published and behind", false, true, plan(stack.Repo{Parent: "P", Behind: true}), false},
		{"published and to retarget", false, true, plan(stack.Repo{Parent: "P", Merged: true, Retarget: true}), false},
		{"closed", true, false, stack.Plan{}, true},
		{"closed and published", true, true, stack.Plan{}, true},
		{"closed whatever the plan says", true, true, plan(stack.Repo{Parent: "P", Behind: true}), true},
		{"unpublished and behind has not ended", false, false, plan(stack.Repo{Parent: "P", Behind: true}), false},
	}
	for _, c := range cases {
		if got := worktreeEnded(c.closed, c.published, c.plan); got != c.want {
			t.Errorf("%s: worktreeEnded = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestWorktreeWanted: the work is wanted once the ticket has a brief and nothing
// holds it; not while it is being briefed or blocked, not when its phase could
// not be told, and not after it ended.
func TestWorktreeWanted(t *testing.T) {
	t.Parallel()
	approved := phase.Facts{
		Ticket: wtTicket, BriefExists: true, Sections: 2, Approval: phase.ApprovalCurrent, ApprovedBy: "Dana", ApprovedHow: "person",
		Slices: []phase.Slice{{ID: "a", State: "queued"}},
	}
	briefing := approved
	briefing.Approval = phase.ApprovalNone
	blocked := approved
	blocked.Blockers = []phase.Blocker{{Ticket: "JIG-6", Kind: "stacked", Reason: "its gate has no clean round", Code: phase.CodeBlocked}}
	derive := func(f phase.Facts) *phase.Ticket {
		t := phase.Derive(f, nil)
		return &t
	}
	cases := []struct {
		name string
		n    nextTicket
		want bool
	}{
		{"building", nextTicket{State: ready.StateReady, derived: derive(approved)}, true},
		{"being briefed", nextTicket{State: ready.StateReady, derived: derive(briefing)}, false},
		{"blocked", nextTicket{State: ready.StateReady, derived: derive(blocked)}, false},
		{"its phase could not be told", nextTicket{State: ready.StateReady}, false},
		{"published", nextTicket{State: ready.StateDone, derived: derive(approved)}, false},
		{"closed", nextTicket{State: ready.StateClosed, derived: derive(approved)}, false},
		{"behind, owed a merge", nextTicket{State: stateBehind, derived: derive(approved)}, true},
	}
	if p := derive(briefing); p.Current != phase.Brief {
		t.Fatalf("test setup: the unapproved ticket is at %s, want brief", p.Current)
	}
	if p := derive(blocked); p.Code != phase.CodeBlocked {
		t.Fatalf("test setup: the blocked ticket has code %q, want %s", p.Code, phase.CodeBlocked)
	}
	for _, c := range cases {
		if got := c.n.worktreeWanted(); got != c.want {
			t.Errorf("%s: worktreeWanted = %v, want %v", c.name, got, c.want)
		}
	}
}

// wtRows are rows of each action, for the renderers.
func wtRows() []nextWorktree {
	return []nextWorktree{
		{Repo: "api", Action: "add", Branch: wtBranch, Clone: "/src/api", Dir: "/src/api.worktrees/JIG-7", Base: "origin/main",
			Command: "git -C /src/api worktree add /src/api.worktrees/JIG-7 -b " + wtBranch + " origin/main"},
		{Repo: "web", Action: "present", Branch: wtBranch, Clone: "/src/web", Dir: "/src/web.worktrees/JIG-7"},
		{Repo: "docs", Action: "no_clone", Branch: wtBranch, Note: "repo docs has no clone on this machine: JIG-7 has no worktree to add there"},
		{Repo: "lib", Action: "remove", Branch: wtBranch, Clone: "/src/lib", Dir: "/src/lib.worktrees/JIG-7",
			Command: "git -C /src/lib worktree remove /src/lib.worktrees/JIG-7"},
	}
}

// TestWorktreeRenderTable: the rows are a worktrees table with a dash for an
// empty cell, and a note line for each repo with no clone follows it.
func TestWorktreeRenderTable(t *testing.T) {
	t.Parallel()
	blocks := worktreeBlocks(wtRows())
	if len(blocks) != 2 {
		t.Fatalf("blocks = %q, want the table and one note", blocks)
	}
	// Quote wraps a cell with a colon, a comma or a backslash only; these
	// paths and commands have none, so the table carries them as they are.
	wantTable := strings.Join([]string{
		"worktrees[4]{repo,action,dir,base,command}:",
		"  api,add,/src/api.worktrees/JIG-7,origin/main,git -C /src/api worktree add /src/api.worktrees/JIG-7 -b " + wtBranch + " origin/main",
		"  web,present,/src/web.worktrees/JIG-7,-,-",
		"  docs,no_clone,-,-,-",
		"  lib,remove,/src/lib.worktrees/JIG-7,-,git -C /src/lib worktree remove /src/lib.worktrees/JIG-7",
	}, "\n")
	if blocks[0] != wantTable {
		t.Errorf("table:\n%s\nwant:\n%s", blocks[0], wantTable)
	}
	if want := "note: repo docs has no clone on this machine: JIG-7 has no worktree to add there"; blocks[1] != want {
		t.Errorf("note = %q, want %q", blocks[1], want)
	}
	if got := worktreeBlocks(nil); len(got) != 0 {
		t.Errorf("no rows rendered %q, want nothing", got)
	}
}

// TestWorktreeRenderHelp: one line for each add and each remove, naming the
// command, and none for a worktree that is there or a repo with no clone.
func TestWorktreeRenderHelp(t *testing.T) {
	t.Parallel()
	want := []string{
		"Add the worktree of JIG-7 in api: `git -C /src/api worktree add /src/api.worktrees/JIG-7 -b " + wtBranch + " origin/main`",
		"Remove the worktree of JIG-7 in lib (git refuses one with changes): `git -C /src/lib worktree remove /src/lib.worktrees/JIG-7`",
	}
	if got := worktreeHelp("JIG-7", wtRows()); !reflect.DeepEqual(got, want) {
		t.Errorf("help = %q, want %q", got, want)
	}
	if got := worktreeHelp("JIG-7", nil); len(got) != 0 {
		t.Errorf("help for no rows = %q", got)
	}
}

// TestWorktreeRenderInNext: `jig next <ticket>` prints the table before the
// topology table, the add lines ahead of the usual help (whose git switch line
// stays), and prints nothing of worktrees when there are no rows; the JSON has
// the rows under worktrees, and omits the key when there are none.
func TestWorktreeRenderInNext(t *testing.T) {
	t.Parallel()
	n := nextTicket{
		Ticket: wtTicket, State: ready.StateReady, Branch: wtBranch,
		Ready:     []nextBatch{{Repo: "api", Slices: []string{"a"}, Start: "origin/main"}},
		Topology:  []nextSlice{{ID: "a", Workspace: "api/lib", Repo: "api", State: "queued", Wave: 1, BlockedBy: []string{}}},
		Worktrees: wtRows(),
	}
	out := strings.Join(n.blocks(true), "\n")
	table, topology := strings.Index(out, "worktrees[4]{repo,action,dir,base,command}:"), strings.Index(out, "topology[")
	if table < 0 || topology < 0 || table > topology {
		t.Errorf("the worktrees table must come before the topology table:\n%s", out)
	}
	help := out[strings.Index(out, "help["):]
	lines := strings.Split(help, "\n")
	if len(lines) < 4 || !strings.HasPrefix(lines[1], "  Add the worktree of JIG-7 in api: ") || !strings.HasPrefix(lines[2], "  Remove the worktree of JIG-7 in lib ") ||
		!strings.Contains(lines[3], "git switch -c "+wtBranch) {
		t.Errorf("help = %q, want the add and remove lines, then the git switch line", lines)
	}
	if !strings.Contains(out, "note: repo docs has no clone on this machine") {
		t.Errorf("the no_clone note is missing:\n%s", out)
	}

	data, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Worktrees []nextWorktree `json:"worktrees"`
	}
	if err := json.Unmarshal(data, &back); err != nil || !reflect.DeepEqual(back.Worktrees, wtRows()) {
		t.Errorf("json worktrees = %+v, %v, want the rows", back.Worktrees, err)
	}
	for _, key := range []string{`"repo":`, `"action":`, `"branch":`, `"clone":`, `"dir":`, `"base":`, `"command":`, `"note":`} {
		if !strings.Contains(string(data), key) {
			t.Errorf("json lacks the key %s: %s", key, data)
		}
	}

	n.Worktrees = nil
	data, err = json.Marshal(n)
	if err != nil || strings.Contains(string(data), "worktrees") {
		t.Errorf("a ticket with no rows marshals %s, want no worktrees key", data)
	}
	if out := strings.Join(n.blocks(true), "\n"); strings.Contains(out, "worktrees[") || strings.Contains(out, "Add the worktree") {
		t.Errorf("a ticket with no rows prints worktrees:\n%s", out)
	}
}

// TestPublishPRWorktrees: the record that finishes a publish also says how to
// remove the ticket's worktrees: their table after the pull request and each
// command ahead of the status line. A record with no remove rows prints what it
// always did, and a record with a repo still pending adds nothing, whatever rows
// the ticket has: the publish is not done.
func TestPublishPRWorktrees(t *testing.T) {
	t.Parallel()
	report := verifydeliver.RecordReport{
		Repo: "api", URL: "https://example.invalid/acme/api/pull/7", Branch: wtBranch, Commit: "0123456789ab", Verified: true,
	}
	remove := nextWorktree{
		Repo: "api", Action: "remove", Branch: wtBranch, Clone: "/src/api", Dir: "/src/api.worktrees/JIG-7",
		Command: "git -C /src/api worktree remove /src/api.worktrees/JIG-7",
	}
	status := "Run `jig status JIG-7` to confirm the ticket is fully green"
	head := strings.Join([]string{
		"publish_pr:",
		"  ticket: JIG-7",
		"  repo: api",
		`  url: "https://example.invalid/acme/api/pull/7"`,
		"  branch: " + wtBranch,
		"  commit: 0123456789ab",
		"  verified: yes",
	}, "\n")

	t.Run("with a worktree to remove", func(t *testing.T) {
		t.Parallel()
		got := recordedBlocks("JIG-7", report, []nextWorktree{remove})
		want := []string{
			head,
			"worktrees[1]{repo,action,dir,base,command}:\n  api,remove,/src/api.worktrees/JIG-7,-,git -C /src/api worktree remove /src/api.worktrees/JIG-7",
			"help[2]:\n  Remove the worktree of JIG-7 in api (git refuses one with changes): `" + remove.Command + "`\n  " + status,
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("blocks:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})
	t.Run("without rows, or with rows that are no removal", func(t *testing.T) {
		t.Parallel()
		want := []string{head, "help[1]:\n  " + status}
		if got := recordedBlocks("JIG-7", report, nil); !reflect.DeepEqual(got, want) {
			t.Errorf("no rows: blocks:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		kept := []nextWorktree{{Repo: "api", Action: "present", Dir: "/src/api.worktrees/JIG-7"}, {Repo: "web", Action: "add", Command: "git -C /src/web worktree add x b"}}
		if got := recordedBlocks("JIG-7", report, kept); !reflect.DeepEqual(got, want) {
			t.Errorf("rows that stay: blocks:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})
	t.Run("with a repo still pending", func(t *testing.T) {
		t.Parallel()
		pending := report
		pending.Pending = []string{"web", "docs"}
		got := recordedBlocks("JIG-7", pending, []nextWorktree{remove})
		want := []string{head, "help[1]:\n  web and docs still needs its pull request: `jig publish JIG-7 --pr <url> --repo web`"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("blocks:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})
}
