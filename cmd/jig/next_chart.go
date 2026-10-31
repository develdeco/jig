package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/recall"
	"github.com/develdeco/jig/internal/store"
)

// nextChart is where a chart stands, as `jig next <chart>` says it and the TUI's
// chart screen reads it (--json): the verdict (Ready, Code, Reason, Prompt), the
// tickets in dependency order with each one's phases and reach, the briefs to
// write and the waves to build, where the run ends, and what map.md says. Every
// key is present but rank, steps, waits_for, prs and map, which are left out
// when there is nothing to say.
type nextChart struct {
	Chart       string            `json:"chart"`
	Title       string            `json:"title"`
	Ready       bool              `json:"ready"`
	Code        string            `json:"code"`
	Reason      string            `json:"reason"`
	Prompt      string            `json:"prompt"`
	Started     bool              `json:"started"`
	Ungraduated int               `json:"ungraduated"`
	Briefs      []string          `json:"briefs"`
	Run         [][]string        `json:"run"`
	Tickets     []nextChartTicket `json:"tickets"`
	Ends        []nextChartEnd    `json:"ends"`
	Map         *nextChartMap     `json:"map,omitempty"`

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

// nextChartMap is the sections of the chart's map.md.
type nextChartMap struct {
	Destination []string `json:"destination"`
	Decided     []string `json:"decided"`
	Fog         []string `json:"fog"`
	OutOfScope  []string `json:"out_of_scope"`
}

// chartOutput lays the standing and the map into what the verbs print.
func chartOutput(c phase.Chart, m chartMap) nextChart {
	out := nextChart{
		Chart: c.Name, Title: m.Title, Ready: c.Ready, Code: c.Code, Reason: c.Reason, Prompt: c.Prompt,
		Started: c.Started, Ungraduated: c.Ungraduated,
		Briefs: orEmpty(c.Briefs), Run: c.Run, Tickets: []nextChartTicket{}, Ends: []nextChartEnd{}, derived: c,
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
func (n nextChart) help() []string { return chartHelp(n.derived) }

// chartHelp says what to do for a chart at its standing: ask the prompt when
// something can run, merge the pull request a run ends at then ask to continue,
// graduate the entries that have no ticket, or what blocks it.
func chartHelp(c phase.Chart) []string {
	graduate := fmt.Sprintf("Run `jig graduate %s` to create the %d missing ticket(s)", c.Name, c.Ungraduated)
	switch {
	case c.Ready:
		help := []string{"Ask: " + c.Prompt}
		if c.Ungraduated > 0 {
			help = append(help, graduate)
		}
		return help
	case c.Code == phase.CodeChartWaitsMerge:
		return []string{fmt.Sprintf("Merge %s, then ask: %s", mergeTargets(c.Ends), c.Prompt)}
	case c.Code == phase.CodeChartNotGraduated && c.Ungraduated > 0:
		return []string{graduate}
	case c.Code == phase.CodeChartDone:
		return []string{c.Name + " is done: every ticket is published or closed; run `jig next` to see what else is ready"}
	}
	return []string{c.Reason}
}

// mergeTargets names what a run waits on to merge: the pull request URLs of its
// ends, else the tickets whose work is to merge.
func mergeTargets(ends []phase.ChartEnd) string {
	var urls, tickets []string
	seen := map[string]bool{}
	for _, e := range ends {
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
