package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/recall"
	"github.com/develdeco/jig/internal/store"
)

// nextChart is where a chart stands, as `jig next <chart>` says it and the TUI's
// chart screen reads it (--json): the verdict (Ready, Code, Reason, Prompt), the
// tickets in dependency order with each one's phases and reach, the briefs to
// write and the waves to build, where the run ends, what map.md says, what the
// run has spent and, for --json, the commits of the store that touched the
// chart's folder. Every key is present but rank, steps, waits_for, prs, map and
// spend, which are left out when there is nothing to say; history is [] when
// the text form is read or the history cannot be.
type nextChart struct {
	Chart       string `json:"chart"`
	Title       string `json:"title"`
	Ready       bool   `json:"ready"`
	Code        string `json:"code"`
	Reason      string `json:"reason"`
	Prompt      string `json:"prompt"`
	Started     bool   `json:"started"`
	Ungraduated int    `json:"ungraduated"`
	// Named echoes the tickets a run was named over (--tickets), in the order of
	// the rows; left out for the whole chart.
	Named   []string          `json:"named,omitempty"`
	Briefs  []string          `json:"briefs"`
	Run     [][]string        `json:"run"`
	Tickets []nextChartTicket `json:"tickets"`
	Ends    []nextChartEnd    `json:"ends"`
	History []nextChartCommit `json:"history"`
	Map     *nextChartMap     `json:"map,omitempty"`
	Spend   *nextChartSpend   `json:"spend,omitempty"`
	// WorkspaceOverlaps are the tickets of one wave that build in one workspace
	// of a repo, the pair in Plan order: the later one is to be stacked on the
	// earlier. Same element as `jig next`'s workspace_overlaps. [] when none.
	WorkspaceOverlaps []nextWorkspaceOverlap `json:"workspace_overlaps"`

	// derived is the same derivation, for the help.
	derived phase.Chart
}

// nextChartTicket is one entry of the chart. An entry with no ticket yet has no
// id, no phase and no steps. Steps are the four phases' states (done, current
// or pending); WaitsFor lists only the needs that are not met.
type nextChartTicket struct {
	Pos      int             `json:"pos"`
	ID       string          `json:"id"`
	Title    string          `json:"title"`
	Rank     *int            `json:"rank,omitempty"`
	Wave     int             `json:"wave"`
	Phase    string          `json:"phase"`
	Steps    []string        `json:"steps,omitempty"`
	Ready    bool            `json:"ready"`
	Code     string          `json:"code"`
	Reach    string          `json:"reach"`
	Prompt   string          `json:"prompt"`
	Problem  string          `json:"problem"`
	WaitsFor []nextChartNeed `json:"waits_for,omitempty"`
	// Worktrees are the worktrees jig advises for the ticket, one for each repo
	// it builds in, as `jig next <ticket>` gives them (worktree_advice.go). Only
	// a ticket of the run has them.
	Worktrees []nextWorktree `json:"worktrees,omitempty"`
}

// nextChartNeed is an edge that is not met: the blocker (a ticket, or the entry
// at Pos when it has none yet), its kind, the code and the fact that says what
// it waits for, and the pull requests it waits on.
type nextChartNeed struct {
	Ticket string        `json:"ticket"`
	Pos    int           `json:"pos"`
	Kind   string        `json:"kind"`
	Code   string        `json:"code"`
	Fact   string        `json:"fact"`
	PRs    []nextChartPR `json:"prs,omitempty"`
}

// nextChartPR is a pull request a merge waits on, in the host's words.
type nextChartPR struct {
	Repo  string `json:"repo"`
	URL   string `json:"url"`
	State string `json:"state"`
}

// nextChartEnd is where the run ends: Ticket waits on Blocker's merge.
type nextChartEnd struct {
	Ticket  string        `json:"ticket"`
	Blocker string        `json:"blocker"`
	Kind    string        `json:"kind"`
	Fact    string        `json:"fact"`
	PRs     []nextChartPR `json:"prs,omitempty"`
}

// nextChartCommit is one commit of the store that changed something under the
// chart's folder: its full hash, its committer time in UTC (RFC 3339) and the
// first line of its message.
type nextChartCommit struct {
	SHA     string `json:"sha"`
	Time    string `json:"time"`
	Subject string `json:"subject"`
}

// chartHistoryLimit is how many of the chart's newest commits `jig next --json`
// carries. The chart screen never shows more lines than the terminal has rows,
// so this is more than a screen holds.
const chartHistoryLimit = 20

// chartHistory is the chart's store history for --json: the newest commits that
// touched charts/<name>/, newest first. It is never nil, and it is empty when
// the history cannot be read (the store is not a git repository, it has no
// commit yet, git fails): a problem is an empty list, never a reason to hide
// the chart, and nothing is printed for it.
func chartHistory(st *store.Store, name string) []nextChartCommit {
	out := []nextChartCommit{}
	entries, err := st.ChartHistory(name, chartHistoryLimit)
	if err != nil {
		return out
	}
	for _, e := range entries {
		out = append(out, nextChartCommit{SHA: e.SHA, Time: e.Time.UTC().Format(time.RFC3339), Subject: e.Subject})
	}
	return out
}

// nextChartMap is the sections of the chart's map.md.
type nextChartMap struct {
	Destination []string `json:"destination"`
	Decided     []string `json:"decided"`
	Fog         []string `json:"fog"`
	OutOfScope  []string `json:"out_of_scope"`
}

// chartOutput lays the standing, the map and the spend (nil when no ticket has
// any) into what the verbs print.
func chartOutput(c phase.Chart, m chartMap, spend *nextChartSpend) nextChart {
	out := nextChart{
		Chart: c.Name, Title: m.Title, Ready: c.Ready, Code: c.Code, Reason: c.Reason, Prompt: c.Prompt,
		Started: c.Started, Ungraduated: c.Ungraduated, Spend: spend,
		Briefs: orEmpty(c.Briefs), Run: c.Run, WorkspaceOverlaps: []nextWorkspaceOverlap{}, Tickets: []nextChartTicket{}, Ends: []nextChartEnd{}, History: []nextChartCommit{}, derived: c,
	}
	if out.Run == nil {
		out.Run = [][]string{}
	}
	for _, r := range c.Rows {
		row := nextChartTicket{
			Pos: r.Pos, ID: r.ID, Title: r.Title, Rank: r.Rank, Wave: r.Wave,
			Reach: r.Reach, Prompt: r.Prompt(), Problem: r.Problem,
		}
		if t := r.Ticket; t != nil {
			row.Phase, row.Ready, row.Code = string(t.Current), t.Ready, t.Code
			for _, p := range t.Phases {
				row.Steps = append(row.Steps, p.State)
			}
		}
		for _, n := range r.Waits {
			row.WaitsFor = append(row.WaitsFor, nextChartNeed{
				Ticket: n.Ticket, Pos: n.Pos, Kind: n.Kind, Code: n.Code, Fact: n.Why, PRs: chartPRs(n.PRs),
			})
		}
		out.Tickets = append(out.Tickets, row)
	}
	for _, e := range c.Ends {
		out.Ends = append(out.Ends, nextChartEnd{Ticket: e.ID, Blocker: e.Because.Ticket, Kind: e.Because.Kind, Fact: e.Because.Why, PRs: chartPRs(e.Because.PRs)})
	}
	if m.Exists {
		out.Map = &nextChartMap{Destination: m.Destination, Decided: m.Decided, Fog: m.Fog, OutOfScope: m.OutOfScope}
	}
	return out
}

// setWorktrees lays on each graduated ticket of the run (any wave) the worktrees
// jig advises for it: a ticket in the run is work of this run, so its work is
// wanted. A ticket whose facts cannot be read gets none and never hides the
// chart. The chart's standing is unchanged; `jig status <chart>` does not call
// this.
func (n *nextChart) setWorktrees(src *worktreeSource) {
	inRun := map[string]bool{}
	for _, wave := range n.Run {
		for _, id := range wave {
			inRun[id] = true
		}
	}
	for i := range n.Tickets {
		t := &n.Tickets[i]
		if t.ID == "" || !inRun[t.ID] {
			continue
		}
		facts, err := src.factsOf(t.ID)
		if err != nil {
			continue
		}
		facts.Wanted = true
		t.Worktrees = src.rows(facts)
	}
}

// setWorkspaceOverlaps lists, for each wave of the run, the tickets that build
// in one workspace of a repo, each pair in Plan order (the entry's position in
// tickets.yaml), by the rule `jig next` warns with (workspaceOverlaps). Only
// tickets still to build are listed: a ticket whose build is done (its current
// phase is past build: reviewed, published or behind) is left out. A pair with
// an edge between them, met or not, is left out too: a wave drops the needs
// that are met, so a stacked pair whose parent is reviewed clean shares a wave
// again, and it is already stacked. A ticket whose slices or edges cannot be
// read has no site and no edge, and never hides the chart.
func (n *nextChart) setWorkspaceOverlaps(st *store.Store, cfg project.Config, mp project.MachineProject) {
	pos := map[string]int{}
	built := map[string]bool{}
	for _, t := range n.Tickets {
		if t.ID == "" {
			continue
		}
		pos[t.ID] = t.Pos
		built[t.ID] = t.Phase == string(phase.Review) || t.Phase == string(phase.Publish)
	}
	slicesOf := map[string][]store.Slice{}
	edges := map[string]map[string]bool{}
	for _, wave := range n.Run {
		ids := make([]string, 0, len(wave))
		for _, id := range wave {
			if !built[id] {
				ids = append(ids, id)
			}
		}
		sort.SliceStable(ids, func(i, j int) bool { return pos[ids[i]] < pos[ids[j]] })
		for _, id := range ids {
			if _, read := slicesOf[id]; read {
				continue
			}
			if slices, err := st.ReadSlices(id); err == nil {
				slicesOf[id] = slices
			}
			if rec, err := st.ReadTicket(id); err == nil {
				edges[id] = map[string]bool{}
				for _, b := range rec.BlockedBy {
					blocker := b.Ticket
					if resolved, err := st.ResolveTicket(b.Ticket); err == nil {
						blocker = resolved
					}
					edges[id][blocker] = true
				}
			}
		}
		for _, o := range workspaceOverlaps(cfg, mp, ids, slicesOf) {
			if edges[o.Tickets[0]][o.Tickets[1]] || edges[o.Tickets[1]][o.Tickets[0]] {
				continue
			}
			n.WorkspaceOverlaps = append(n.WorkspaceOverlaps, o)
		}
	}
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func chartPRs(prs []phase.MergePR) []nextChartPR {
	var out []nextChartPR
	for _, p := range prs {
		out = append(out, nextChartPR{Repo: p.Repo, URL: p.URL, State: p.State})
	}
	return out
}

// blocks are the text form: the verdict, the tickets in order, the briefs and
// the waves to run, where the run ends, and the help. label heads the verdict
// block (`next` for `jig next`, `status` for `jig status`).
func (n nextChart) blocks(label string) []string {
	pairs := [][2]string{{"chart", n.Chart}}
	add := func(k, v string) {
		if v != "" {
			pairs = append(pairs, [2]string{k, v})
		}
	}
	add("title", n.Title)
	pairs = append(pairs, [2]string{"ready", yesNo(n.Ready)})
	add("prompt", n.Prompt)
	add("code", n.Code)
	add("reason", n.Reason)
	pairs = append(pairs, [2]string{"started", yesNo(n.Started)}, [2]string{"ungraduated", strconv.Itoa(n.Ungraduated)})
	if len(n.Named) > 0 {
		pairs = append(pairs, [2]string{"named", joinPlus(n.Named)})
	}
	if n.Spend != nil {
		pairs = append(pairs, [2]string{"spend", n.Spend.line()})
	}
	blocks := []string{axi.KV(label, pairs)}

	var rows [][]string
	for _, t := range n.Tickets {
		id, steps, waits := t.ID, "-", "-"
		if id == "" {
			id = "-"
		}
		if len(t.Steps) > 0 {
			steps = ""
			for _, s := range t.Steps {
				steps += s[:1]
			}
		}
		if len(t.WaitsFor) > 0 {
			cells := make([]string, len(t.WaitsFor))
			for i, w := range t.WaitsFor {
				who := w.Ticket
				if who == "" {
					who = fmt.Sprintf("#%d", w.Pos)
				}
				cells[i] = who + "(" + w.Kind + ")"
			}
			waits = strings.Join(cells, "+")
		}
		rows = append(rows, []string{id, t.Title, steps, t.Reach, waits})
	}
	blocks = append(blocks, axi.Table("tickets", []string{"id", "title", "steps", "reach", "waits_for"}, rows))
	// What the JSON carries as a row's problem: an entry whose ref does not
	// resolve, or a ticket that cannot be read.
	var problems [][]string
	for _, t := range n.Tickets {
		if t.Problem == "" {
			continue
		}
		problems = append(problems, []string{strconv.Itoa(t.Pos), dashed(t.ID), t.Problem})
	}
	if len(problems) > 0 {
		blocks = append(blocks, axi.Table("problems", []string{"pos", "id", "problem"}, problems))
	}
	if len(n.Briefs) > 0 {
		var brows [][]string
		for _, id := range n.Briefs {
			brows = append(brows, []string{id})
		}
		blocks = append(blocks, axi.Table("briefs", []string{"ticket"}, brows))
	}
	if len(n.Run) > 0 {
		var wrows [][]string
		for i, w := range n.Run {
			wrows = append(wrows, []string{strconv.Itoa(i + 1), joinPlus(w)})
		}
		blocks = append(blocks, axi.Table("run", []string{"wave", "tickets"}, wrows))
	}
	if len(n.WorkspaceOverlaps) > 0 {
		var orows [][]string
		for _, o := range n.WorkspaceOverlaps {
			orows = append(orows, []string{o.Repo, o.Workspace, joinPlus(o.Tickets)})
		}
		blocks = append(blocks, "warning: tickets of one wave build in the same workspace",
			axi.Table("workspace_overlap", []string{"repo", "workspace", "tickets"}, orows))
	}
	var worktrees [][]string
	for _, t := range n.Tickets {
		for _, w := range t.Worktrees {
			worktrees = append(worktrees, append([]string{t.ID}, worktreeCells(w)...))
		}
	}
	if len(worktrees) > 0 {
		blocks = append(blocks, axi.Table("worktrees", []string{"ticket", "repo", "action", "dir", "base", "command"}, worktrees))
	}
	if len(n.Ends) > 0 {
		var erows [][]string
		for _, e := range n.Ends {
			if len(e.PRs) == 0 {
				erows = append(erows, []string{e.Ticket, dashed(e.Blocker), e.Kind, "-", "-", e.Fact})
			}
			for _, p := range e.PRs {
				erows = append(erows, []string{e.Ticket, dashed(e.Blocker), e.Kind, p.URL, dashed(p.State), e.Fact})
			}
		}
		blocks = append(blocks, axi.Table("waits", []string{"ticket", "blocker", "kind", "pr", "state", "fact"}, erows))
	}
	return append(blocks, axi.Help(n.help()...))
}

// help is the next step for the chart: the one chartHelp says, from the same
// derivation, so `jig next` and `jig status` never name different steps.
func (n nextChart) help() []string {
	help := chartHelp(n.derived)
	if len(n.WorkspaceOverlaps) > 0 {
		help = append(help, "Stack the later ticket of each `workspace_overlap` pair on the earlier: add `blocked_by` with `kind: stacked` to its `ticket.yaml`, check it with `jig validate <T> --check`, then run `jig next <chart>` again")
	}
	return help
}

// chartHelp says what to do for a chart at its standing: ask the prompt when
// something can run, merge the pull request a run ends at then ask to continue,
// graduate the entries that have no ticket (validate them first while one has a
// problem, which graduate would refuse), or what blocks it.
func chartHelp(c phase.Chart) []string {
	graduate := fmt.Sprintf("Run `jig graduate %s` to create the %d missing ticket(s)", c.Name, c.Ungraduated)
	if c.HasUngraduatedProblem() {
		graduate = fmt.Sprintf("Run `jig validate %s` to list the problems of the entries that have no ticket yet, fix them in charts/%s/tickets.yaml, then run `jig graduate %s`", c.Name, c.Name, c.Name)
	}
	switch {
	case c.Ready:
		help := []string{"Ask: " + c.Prompt}
		if c.Ungraduated > 0 {
			help = append(help, graduate)
		}
		return help
	case c.Code == phase.CodeChartWaitsMerge:
		help := []string{fmt.Sprintf("Merge %s, then ask: %s", mergeTargets(c.RootEnds()), c.Prompt)}
		if name := notNamedHelp(c.RootEnds()); name != "" {
			help = append(help, name)
		}
		return help
	case c.Code == phase.CodeNeedNotNamed && notNamedHelp(c.RootEnds()) != "":
		return []string{notNamedHelp(c.RootEnds())}
	case c.Code == phase.CodeChartNotGraduated && c.Ungraduated > 0:
		return []string{graduate}
	case c.Code == phase.CodeChartDone && len(c.Named) > 0:
		are := " are"
		if len(c.Named) == 1 {
			are = " is"
		}
		return []string{andList(c.Named) + are + " done: each is published or closed; run `jig next " + c.Name + "` for the whole chart"}
	case c.Code == phase.CodeChartDone:
		return []string{c.Name + " is done: every ticket is published or closed; run `jig next` to see what else is ready"}
	}
	return []string{c.Reason}
}

// notNamedHelp is the step for a named ticket that waits on a ticket the run
// does not take: no merge frees it, so name that ticket too or take it first.
// "" when no end is such a wait.
func notNamedHelp(ends []phase.ChartEnd) string {
	for _, e := range ends {
		if e.Because.Code != phase.CodeNeedNotNamed {
			continue
		}
		a := e.Because.Ticket
		if a == "" {
			a = fmt.Sprintf("entry #%d", e.Because.Pos)
		}
		return fmt.Sprintf("Name %s too, or take %s first", a, a)
	}
	return ""
}

// mergeTargets names what a run waits on to merge: the pull request URLs of its
// ends, else the tickets whose work is to merge. A wait on a ticket the run does
// not take is no merge.
func mergeTargets(ends []phase.ChartEnd) string {
	var urls, tickets []string
	seen := map[string]bool{}
	for _, e := range ends {
		if e.Because.Code == phase.CodeNeedNotNamed {
			continue
		}
		for _, p := range e.Because.PRs {
			if !seen[p.URL] {
				seen[p.URL] = true
				urls = append(urls, p.URL)
			}
		}
		if b := e.Because.Ticket; b != "" && !seen[b] {
			seen[b] = true
			tickets = append(tickets, b)
		}
	}
	switch {
	case len(urls) > 0:
		return andList(urls)
	case len(tickets) > 0:
		return "the work of " + andList(tickets)
	}
	return "the pull request it waits on"
}

// nextChartRow is one chart in the listing `jig next` prints for the store: its
// name, its title (map.md's first heading), the ids of its graduated tickets and
// how many entries have no ticket yet. It reads tickets.yaml and the map's
// heading only; the standing is `jig next <chart>`.
type nextChartRow struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Tickets     []string `json:"tickets"`
	Ungraduated int      `json:"ungraduated"`
}

// chartRows lists the store's charts. A chart whose tickets.yaml cannot be read
// is listed with no tickets rather than left out.
func chartRows(st *store.Store) []nextChartRow {
	names, err := st.ChartNames()
	if err != nil {
		return nil
	}
	var out []nextChartRow
	for _, name := range names {
		row := nextChartRow{Name: name, Tickets: []string{}}
		if data, err := os.ReadFile(st.ChartMapFile(name)); err == nil {
			row.Title = recall.MapTitle(string(data))
		}
		if entries, err := st.ReadChart(name); err == nil {
			for _, e := range entries {
				if e.ID == "" {
					row.Ungraduated++
				} else {
					row.Tickets = append(row.Tickets, e.ID)
				}
			}
		}
		out = append(out, row)
	}
	return out
}

// chartBlocks is the charts table of the store listing, nothing when there are
// no charts.
func chartBlocks(charts []nextChartRow) []string {
	if len(charts) == 0 {
		return nil
	}
	var rows [][]string
	for _, c := range charts {
		tickets := "-"
		if len(c.Tickets) > 0 {
			tickets = joinPlus(c.Tickets)
		}
		rows = append(rows, []string{c.Name, c.Title, tickets})
	}
	return []string{axi.Table("charts", []string{"name", "title", "tickets"}, rows)}
}
