package main

import (
	"fmt"
	"io"
	"sort"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/mergefacts"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// init registers `jig here`'s conflicts section (sections.go): the files the
// located ticket's branch cannot merge with another open ticket's, shown where
// the session looks first. It is advice, never a refusal.
func init() {
	registerHereSection(hereSection{
		Order: 320,
		Name:  "conflicts",
		Blocks: func(c hereCtx) ([]string, error) {
			a, ok := hereConflicts(c)
			if !ok {
				return nil, nil
			}
			return conflictBlocks(a, true), nil
		},
	})
}

// ticketConflict is one file the branch of the ticket in hand cannot merge
// with the branch of another open ticket: the repo, the other ticket, the file
// git reports unmerged and the other ticket's branch.
type ticketConflict struct {
	Repo   string `json:"repo"`
	Ticket string `json:"ticket"`
	File   string `json:"file"`
	Branch string `json:"branch"`
	// First is the ticket of the pair that comes first in `jig next` order.
	First string `json:"first,omitempty"`
}

// cleanShare is a file the ticket in hand shares with another ticket whose
// branch merges with it cleanly.
type cleanShare struct {
	Repo   string `json:"repo"`
	Ticket string `json:"ticket"`
	File   string `json:"file"`
}

// conflictAdvice is what the ticket in hand is told about the other open
// tickets' branches.
type conflictAdvice struct {
	Ticket    string
	Conflicts []ticketConflict
	Clean     []cleanShare
}

// ticketConflictAdvice tests the branch of ticket against the branches of the
// other open tickets that share a changed file with it (scanOverlaps tests
// each such pair once). A pair that could not be tested says nothing.
func ticketConflictAdvice(st *store.Store, cfg project.Config, mp project.MachineProject, ticket string, merges mergeSource) conflictAdvice {
	_, pairs := scanOverlaps(st, cfg, mp, ticketOpenFilter(st, merges), ticket)
	return adviceFromPairs(st, pairs, ticket)
}

func adviceFromPairs(st *store.Store, pairs []testedPair, ticket string) conflictAdvice {
	a := conflictAdvice{Ticket: ticket}
	for _, p := range pairs {
		other, otherBranch := p.A, p.BranchA
		switch ticket {
		case p.A:
			other, otherBranch = p.B, p.BranchB
		case p.B:
		default:
			continue
		}
		switch p.State {
		case pairConflict:
			first := firstInNextOrder(st, p.A, p.B)
			for _, f := range p.Files {
				a.Conflicts = append(a.Conflicts, ticketConflict{Repo: p.Repo, Ticket: other, File: f, Branch: otherBranch, First: first})
			}
		case pairClean:
			if len(p.Shared) > 0 {
				a.Clean = append(a.Clean, cleanShare{Repo: p.Repo, Ticket: other, File: p.Shared[0]})
			}
		}
	}
	return a
}

// firstInNextOrder is the ticket of a and b that `jig next` lists first: by
// rank, lowest first and the unranked after, then by id.
func firstInNextOrder(st *store.Store, a, b string) string {
	var in []ready.Ranked
	for _, id := range []string{a, b} {
		r := ready.Ranked{ID: id}
		if rec, err := st.ReadTicket(id); err == nil {
			r.Rank = rec.Rank
		}
		in = append(in, r)
	}
	sort.Slice(in, func(i, j int) bool { return in[i].ID < in[j].ID })
	return ready.ByPriority(in)[0]
}

// conflictBlocks is the warning, the table and, when withClean, one info line
// for each clean sharing pair. The gate and publish pass false: a clean pair
// prints nothing there.
func conflictBlocks(a conflictAdvice, withClean bool) []string {
	var blocks []string
	if len(a.Conflicts) > 0 {
		rows := make([][]string, len(a.Conflicts))
		for i, c := range a.Conflicts {
			rows[i] = []string{c.Repo, c.Ticket, c.File, c.Branch}
		}
		blocks = append(blocks,
			fmt.Sprintf("warning: the branch of %s cannot merge with the branch of another open ticket", a.Ticket),
			axi.Table("conflict", []string{"repo", "ticket", "file", "branch"}, rows))
	}
	if withClean {
		for _, c := range a.Clean {
			blocks = append(blocks, fmt.Sprintf("info: %s shares %s with %s; merges cleanly", a.Ticket, c.File, c.Ticket))
		}
	}
	return blocks
}

// conflictHelp is the help of a ticket with conflicts: the later ticket of
// each pair is stacked on the one that comes first in `jig next` order.
func conflictHelp(ticket string, conflicts []ticketConflict) []string {
	var help []string
	seen := map[string]bool{}
	for _, c := range conflicts {
		if seen[c.Ticket] {
			continue
		}
		seen[c.Ticket] = true
		if c.First == c.Ticket {
			help = append(help, fmt.Sprintf("Stack it with `jig depend %s --on %s --kind stacked`, or wait for %s to merge", ticket, c.Ticket, c.Ticket))
		} else {
			help = append(help, fmt.Sprintf("%s comes first: stack %s on it with `jig depend %s --on %s --kind stacked`, or wait for %s to merge", ticket, c.Ticket, c.Ticket, ticket, ticket))
		}
	}
	return help
}

// hereConflicts is the advice for the located ticket; ok is false when `jig
// here` located no store, no ticket or no mapped project, or the project
// cannot be read: this is advice, so it then says nothing.
func hereConflicts(c hereCtx) (conflictAdvice, bool) {
	if m := c.memo; m != nil {
		if !m.done {
			m.advice, m.ok = readHereConflicts(c)
			m.done = true
		}
		return m.advice, m.ok
	}
	return readHereConflicts(c)
}

// hereMemo is the advice of one `jig here`, read once.
type hereMemo struct {
	done   bool
	ok     bool
	advice conflictAdvice
}

func readHereConflicts(c hereCtx) (conflictAdvice, bool) {
	if c.Store == nil || c.Where.Ticket == "" {
		return conflictAdvice{}, false
	}
	cfg, mp, ok := locatedProject(c.Where, c.JigHome)
	if !ok {
		return conflictAdvice{}, false
	}
	return ticketConflictAdvice(c.Store, cfg, mp, c.Where.Ticket, cachedMergeReader(c.Store, cfg, mp, c.JigHome)), true
}

// locatedProject is the project config and the machine's mapping of it for
// where; ok is false when either cannot be read.
func locatedProject(where locate.Where, jigHome string) (project.Config, project.MachineProject, bool) {
	cfg, err := project.LoadConfig(where.JigHome, where.StorePath, where.Project, where.Root, where.RepoDir)
	if err != nil {
		return project.Config{}, project.MachineProject{}, false
	}
	machine, err := project.LoadMachine(jigHome)
	if err != nil {
		return project.Config{}, project.MachineProject{}, false
	}
	return cfg, machine[cfg.Name], true
}

// printConflicts tells the session, before a gate runs its oracle or a publish
// pushes, which other open tickets' branches the ticket's cannot merge with:
// the same warning, table and help as `jig here`, and nothing for a clean
// pair. It is advice: it changes no exit code, round or verdict.
func printConflicts(stdout io.Writer, st *store.Store, cfg project.Config, mp project.MachineProject, merges mergeSource, ticket string) {
	a := ticketConflictAdvice(st, cfg, mp, ticket, merges)
	blocks := conflictBlocks(a, false)
	if len(blocks) == 0 {
		return
	}
	axi.Render(stdout, append(blocks, axi.Help(conflictHelp(ticket, a.Conflicts)...))...)
}

// ticketOpenFilter says whether a ticket is open: it is not closed, and its
// work has not merged. A branch bound to a closed or merged ticket is a
// leftover and says nothing about a conflict. A ticket that has not published
// is open. A published one is open only when the pull request states say one
// of its pull requests is open: a state they cannot tell (no pull request
// recorded, none cached, the host not asked) leaves it out, the way a pair
// that cannot be tested says nothing, so a session start is never told to
// stack on work that merged long ago. It reads the recorded pull requests and
// their states alone - not the clone's branches or the slices the full merge
// fact reads - because it runs for every branch of the clone at every session
// start. merges is the source of those states (a merge reader); nil, or one
// with none, leaves every published ticket out. A ticket whose record cannot
// be read is not open.
func ticketOpenFilter(st *store.Store, merges mergeSource) func(string) bool {
	var prs mergefacts.PRs
	if src, ok := merges.(interface{ PullRequests() mergefacts.PRs }); ok {
		prs = src.PullRequests()
	}
	known := map[string]bool{}
	return func(id string) bool {
		if open, ok := known[id]; ok {
			return open
		}
		open := false
		if rec, err := st.ReadTicket(id); err == nil && rec.Closed == nil {
			open = true
			if published, err := isPublished(st, id); err == nil && published {
				open = publishedWorkIsOpen(st, prs, id)
			}
		}
		known[id] = open
		return open
	}
}

// publishedWorkIsOpen says a pull request of the published ticket is open.
func publishedWorkIsOpen(st *store.Store, prs mergefacts.PRs, id string) bool {
	if prs == nil {
		return false
	}
	urls, err := journal.PRURLs(st, id)
	if err != nil {
		return false
	}
	for _, url := range urls {
		if prs.Read(url).State == ready.PROpen {
			return true
		}
	}
	return false
}
