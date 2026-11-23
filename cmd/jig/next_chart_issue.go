package main

import (
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// A model chart's standing carries, besides a legacy chart's, the landings the
// run waits for, the multi-tasks to publish and the issue's own standing
// (ADR 0064). A legacy chart prints none of these keys.

// nextChartLand is a landing the run waits for: Ticket, asked as Prompt once
// the tickets After it have landed.
type nextChartLand struct {
	Ticket string   `json:"ticket"`
	Prompt string   `json:"prompt"`
	After  []string `json:"after"`
	State  string   `json:"state"`
}

// nextChartPublish is a multi-task the main session publishes itself.
type nextChartPublish struct {
	Ticket string `json:"ticket"`
	Prompt string `json:"prompt"`
}

// nextChartIssue is the issue's own standing: Stage is where the run is in its
// tail (landing, suite, fix, unmet, publish, published or landed), State what
// `jig next <issue>` says of its pull request (waiting, ready, published or
// landed), and the rest what that ticket's own `jig next` has always said: its
// branch and where its pull request goes, the children not landed, what is
// unmet, the suite waivers, the issues it is suggested to land after. Worktrees
// are where the suite is run (the stage suite only).
type nextChartIssue struct {
	Ticket     string         `json:"ticket"`
	Stage      string         `json:"stage"`
	State      string         `json:"state"`
	Code       string         `json:"code,omitempty"`
	Why        string         `json:"why,omitempty"`
	Prompt     string         `json:"prompt,omitempty"`
	Branch     string         `json:"branch,omitempty"`
	Stack      []nextStack    `json:"stack,omitempty"`
	Waits      []nextWait     `json:"waits,omitempty"`
	Unmet      []nextUnmet    `json:"unmet,omitempty"`
	Waived     []string       `json:"waived,omitempty"`
	LandsAfter []nextAfterRow `json:"lands_after,omitempty"`
	Worktrees  []nextWorktree `json:"worktrees,omitempty"`
	help       []string
	nt         nextTicket
}

// layModelRows lays the landings and the publishes of c on the output.
func (n *nextChart) layModelRows(c phase.Chart) {
	if !c.Model {
		return
	}
	n.Model = true
	n.Lands, n.Publish = []nextChartLand{}, []nextChartPublish{}
	for _, l := range c.Lands {
		n.Lands = append(n.Lands, nextChartLand{Ticket: l.ID, Prompt: l.Prompt, After: orEmpty(l.After), State: l.State})
	}
	for _, p := range c.Publish {
		n.Publish = append(n.Publish, nextChartPublish{Ticket: p.ID, Prompt: p.Prompt})
	}
}

// setIssue lays the issue block on a model chart: the issue's `jig next` and
// where the run stands in its tail. An issue that cannot be read leaves no
// block, and never hides the chart.
func (n *nextChart) setIssue(st *store.Store, cfg project.Config, mp project.MachineProject, merges mergeSource) {
	c := n.derived
	if !c.Model || c.Issue == nil {
		return
	}
	nt, err := nextForTicket(st, cfg, mp, c.Issue.ID, "", false, "", merges)
	if err != nil {
		return
	}
	is := &nextChartIssue{
		Ticket: c.Issue.ID, Stage: c.Issue.Stage, State: nt.State, Code: c.Issue.Code, Why: c.Issue.Why, Prompt: nt.Prompt,
		Branch: nt.Branch, Stack: nt.Stack, Waits: nt.Waits, Unmet: nt.Unmet, Waived: nt.Waived, LandsAfter: nt.LandsAfter, nt: nt,
	}
	if is.Prompt == "" {
		is.Prompt = c.Issue.Prompt
	}
	if c.Issue.Stage == phase.IssueSuite {
		// The suite is run in a worktree of each repo on the issue's branch.
		src := newWorktreeSource(st, cfg, mp, merges)
		if facts, err := src.factsOf(c.Issue.ID); err == nil {
			facts.Wanted = true
			is.Worktrees = src.rows(facts)
		}
	}
	is.help = suiteHelp(nt)
	n.Issue = is
}

// modelBlocks are the tables a model chart adds to its text form: the landings,
// the multi-tasks to publish, and the issue block with what its own `jig next`
// prints.
func (n nextChart) modelBlocks() []string {
	var blocks []string
	if len(n.Lands) > 0 {
		var rows [][]string
		for _, l := range n.Lands {
			after := "-"
			if len(l.After) > 0 {
				after = joinPlus(l.After)
			}
			rows = append(rows, []string{l.Ticket, l.Prompt, after, l.State})
		}
		blocks = append(blocks, axi.Table("lands", []string{"ticket", "prompt", "after", "state"}, rows))
	}
	if len(n.Publish) > 0 {
		var rows [][]string
		for _, p := range n.Publish {
			rows = append(rows, []string{p.Ticket, p.Prompt})
		}
		blocks = append(blocks, axi.Table("publish", []string{"ticket", "prompt"}, rows))
	}
	is := n.Issue
	if is == nil {
		return blocks
	}
	pairs := [][2]string{{"ticket", is.Ticket}, {"stage", is.Stage}, {"state", is.State}}
	for _, kv := range [][2]string{{"prompt", is.Prompt}, {"code", is.Code}, {"why", is.Why}, {"branch", is.Branch}} {
		if kv[1] != "" {
			pairs = append(pairs, kv)
		}
	}
	blocks = append(blocks, axi.KV("issue", pairs))
	blocks = append(blocks, stackBlocks(is.Ticket, is.Stack)...)
	if len(is.Waits) > 0 {
		rows := make([][]string, len(is.Waits))
		for i, w := range is.Waits {
			rows[i] = []string{w.Ticket, w.Standing}
		}
		blocks = append(blocks, axi.Table("issue_waits", []string{"ticket", "standing"}, rows))
	}
	if len(is.Unmet) > 0 {
		rows := make([][]string, len(is.Unmet))
		for i, u := range is.Unmet {
			rows[i] = []string{u.Code, u.Why, u.Prompt}
		}
		blocks = append(blocks, axi.Table("issue_unmet", []string{"code", "why", "prompt"}, rows))
	}
	if len(is.Waived) > 0 {
		blocks = append(blocks, axi.KV("issue_waived", waivedRows(is.Waived)))
	}
	blocks = append(blocks, afterBlock(is.LandsAfter)...)
	if len(is.Worktrees) > 0 {
		var rows [][]string
		for _, w := range is.Worktrees {
			rows = append(rows, append([]string{is.Ticket}, worktreeCells(w)...))
		}
		blocks = append(blocks, axi.Table("issue_worktrees", []string{"ticket", "repo", "action", "dir", "base", "command"}, rows))
	}
	return blocks
}

// landHelp is the step for a run that stops at a landing: the landing to ask
// for, the merge it may be made on the host instead, and the prompt to go on
// with.
func landHelp(c phase.Chart) []string {
	first := c.Lands[0]
	for _, l := range c.Lands {
		if len(l.After) == 0 {
			first = l
			break
		}
	}
	return []string{first.Prompt + " (or merge it on the host), then ask: " + c.Prompt}
}

// issueHelp are the lines a model chart's issue block adds to the help: the
// fix task after a failed suite, the note about CI after a suite step.
func (n nextChart) issueHelp() []string {
	if n.Issue == nil {
		return nil
	}
	help := n.Issue.help
	switch n.Issue.Stage {
	case phase.IssueSuite:
		if len(n.Issue.Worktrees) > 0 {
			rows := make([]string, 0, len(n.Issue.Worktrees))
			for _, w := range n.Issue.Worktrees {
				rows = append(rows, w.Repo)
			}
			help = append([]string{"Run `jig verify " + n.Issue.Ticket + " --final` in a worktree of " + strings.Join(rows, ", ") + " on " + n.Issue.Branch + ": the issue's suite is due before its pull request"}, help...)
		}
	case phase.IssuePublish:
		help = append([]string{"Run `jig publish " + n.Issue.Ticket + " --yes` after the one yes for the group: " + n.Issue.Prompt}, help...)
	case phase.IssuePublished:
		help = append([]string{n.Issue.Ticket + " has its pull request: the run ends at Land " + n.Issue.Ticket}, help...)
	}
	return help
}
