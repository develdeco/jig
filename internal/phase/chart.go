package phase

import (
	"fmt"
	"sort"
	"strings"
)

// Where a chart stands is derived here, as where a ticket stands is: from
// facts a reader gathered (each entry of the chart with the ticket it minted,
// the edges that hold it and whether each is met), never from a record of a run
// (ADR 0033: the span of a run is the prompt). `jig next <chart>`, `jig status
// <chart>` and the TUI's chart screen all read this one answer.

// The chart codes: why the chart cannot be taken on in this run. Like the
// ticket codes, they are the same wherever they are read.
const (
	// CodeChartDone: every ticket of the chart is published or closed.
	CodeChartDone = "CHART_DONE"
	// CodeChartWaitsMerge: nothing can start in this run and a pull request
	// has to merge first; "Continue <chart>" resumes once it has.
	CodeChartWaitsMerge = "CHART_WAITS_MERGE"
	// CodeChartBlocked: nothing can start and no merge frees it.
	CodeChartBlocked = "CHART_BLOCKED"
	// CodeChartNotGraduated: the chart has entries with no ticket yet and
	// nothing else to run: `jig graduate <chart>` mints them.
	CodeChartNotGraduated = "CHART_NOT_GRADUATED"
)

// CodeNeedUnknown is the code of a need whose blocker is no ticket of the store
// (as a claim would find no ticket to read): it can never be met.
const CodeNeedUnknown = "CHART_BLOCKER_UNKNOWN"

// claimBlockerNotPlanned is the claim's code for a blocker closed as not
// planned (cmd/jig/claim_checks.go): the ticket needs planning again, so the
// need can never be met.
const claimBlockerNotPlanned = "CLAIM_BLOCKER_NOT_PLANNED"

// The reach of an entry: whether it can be taken to its pull request in this
// run.
const (
	// ReachDone: published or closed.
	ReachDone = "done"
	// ReachHere: every unmet need is a stacked edge to an entry that is itself
	// reachable here (or there is none).
	ReachHere = "here"
	// ReachAfterMerge: an unmet merged edge, or a need on an entry that is
	// after a merge. The run ends at it; "Continue <chart>" resumes.
	ReachAfterMerge = "after-merge"
	// ReachBlocked: a need that can never be met (closed as not planned, no
	// such ticket, a stacked parent outside the chart, a cycle, a ticket that
	// cannot be read) or an entry behind one.
	ReachBlocked = "blocked"
)

// ChartNeed is one blocked_by edge of an entry, as the reader judged it.
type ChartNeed struct {
	// Ticket is the blocker's id, "" when it is an entry with no id yet (Pos).
	Ticket string
	// Pos is that entry's 1-based position in the chart, set when Ticket is "".
	Pos int
	// Kind is "merged" (or "") or "stacked".
	Kind string
	Met  bool
	// Code and Why say what the need waits for, when it is not met: the
	// claim's code and its sentence.
	Code, Why string
	// PRs are the pull requests a merged need waits on, once the blocker
	// published and they are recorded.
	PRs []MergePR
}

// stacked says the need is a stacked edge: met by the parent's clean gate
// round, so the child builds beside the parent's own run.
func (n ChartNeed) stacked() bool { return n.Kind == "stacked" }

// ChartEntry is one entry of the chart's tickets.yaml with what the reader
// found for it.
type ChartEntry struct {
	// Pos is the 1-based position in tickets.yaml.
	Pos int
	// ID is the ticket the entry minted, "" until `jig graduate`.
	ID    string
	Title string
	// Rank is the ticket's rank, nil when unranked or not graduated.
	Rank *int
	// Needs are every edge of the entry, met or not.
	Needs []ChartNeed
	// Ticket is where the ticket stands, nil when the entry has no ticket or
	// it could not be read (Problem says why).
	Ticket  *Ticket
	Problem string
}

// ChartFacts are what the reader gathered of a chart.
type ChartFacts struct {
	Name    string
	Entries []ChartEntry
}

// ChartRow is one entry in the chart's standing.
type ChartRow struct {
	Pos   int
	ID    string
	Title string
	Rank  *int
	// Wave is the longest chain of in-chart blockers over every edge, 0 for
	// none, -1 for an entry on a cycle or waiting on one (the rule of
	// internal/ready's Waves, over tickets instead of slices).
	Wave   int
	Ticket *Ticket
	Done   bool
	Reach  string
	// Waits are the needs not met, nothing for a done entry.
	Waits   []ChartNeed
	Problem string
}

// ChartEnd is where a run ends: row Pos waits on Because, a merged edge.
type ChartEnd struct {
	Pos     int
	ID      string
	Because ChartNeed
}

// Chart is where a chart stands.
type Chart struct {
	Name string
	// Rows are in dependency order: Wave ascending (every in-chart edge), then
	// rank ascending (unranked last), then position; entries on a cycle last.
	Rows []ChartRow
	// Briefs are the ids of the graduated entries, reachable here or after a
	// merge, whose current phase is the brief: the run briefs them all first.
	Briefs []string
	// Run are the ids of the graduated entries not done and reachable here, in
	// build waves over their UNMET in-chart edges: no edge runs inside one
	// wave, so its tickets build in parallel, and a later wave is stacked on an
	// earlier one.
	Run [][]string
	// Ends are the entries after a merge because of their own unmet merged
	// edge (not through another entry), with the pull requests it waits on.
	Ends []ChartEnd
	// Started says the run began before: a graduated ticket's brief is done
	// (so is any ticket past it). It is the line between "Take" and
	// "Continue".
	Started bool
	// Ungraduated counts the entries with no id yet.
	Ungraduated int
	// Ready says there is something to do in this run: Briefs or Run.
	Ready bool
	// Code and Reason say why not, when it is not ready (and, when it is, that
	// entries have no ticket yet). Prompt is what to ask: the chart's own
	// prompt, "" when nothing can be asked yet.
	Code, Reason, Prompt string
}

// TakePrompt is what to ask to take a chart's tickets to their pull requests
// from the start.
func TakePrompt(chart string) string { return "Take " + chart + " to PR" }

// ContinuePrompt is what to ask to resume a chart that was started: after a
// merge freed a ticket, or in a new session.
func ContinuePrompt(chart string) string { return "Continue " + chart }

// Prompt is what to ask an agent for the entry: the prompt of the phase its
// ticket is at, "" for an entry that is done or has no ticket. The prompt that
// would name another ticket's phase (a build held on a blocker) is the chart's
// to give, not the entry's, unless the entry builds in this run.
func (r ChartRow) Prompt() string {
	t := r.Ticket
	switch {
	case t == nil || r.Done:
		return ""
	case t.Code == CodeBlocked && r.Reach != ReachHere:
		return ""
	}
	return Prompt(t.Current, r.ID)
}

// DeriveChart works out where a chart stands. The reach of an entry (the
// strongest of its unmet needs, blocked over after-merge over here) is
// memoized and guards against cycles; the verdict follows from the rows.
func DeriveChart(f ChartFacts) Chart {
	d := &chartDeriver{
		f:     f,
		byPos: map[int]int{},
		byID:  map[string]int{},
		reach: make([]string, len(f.Entries)),
		why:   make([]string, len(f.Entries)),
		state: make([]int, len(f.Entries)),
		wave:  make([]int, len(f.Entries)),
		wstat: make([]int, len(f.Entries)),
	}
	for i, e := range f.Entries {
		d.byPos[e.Pos] = i
		if e.ID != "" {
			d.byID[e.ID] = i
		}
	}

	c := Chart{Name: f.Name}
	for i := range f.Entries {
		d.reachOf(i)
		e := f.Entries[i]
		row := ChartRow{
			Pos: e.Pos, ID: e.ID, Title: e.Title, Rank: e.Rank, Wave: d.waveOf(i),
			Ticket: e.Ticket, Done: d.done(i), Reach: d.reach[i], Problem: e.Problem,
		}
		if !row.Done {
			for _, n := range e.Needs {
				if !n.Met {
					row.Waits = append(row.Waits, n)
				}
			}
		}
		c.Rows = append(c.Rows, row)
		if e.ID == "" {
			c.Ungraduated++
		}
	}
	sort.SliceStable(c.Rows, func(a, b int) bool { return rowBefore(c.Rows[a], c.Rows[b]) })

	d.runWaves(&c)
	for _, r := range c.Rows {
		if r.ID == "" || r.Done {
			continue
		}
		if r.Ticket != nil && r.Ticket.Current == Brief && (r.Reach == ReachHere || r.Reach == ReachAfterMerge) {
			c.Briefs = append(c.Briefs, r.ID)
		}
		if r.Reach != ReachAfterMerge {
			continue
		}
		for _, n := range r.Waits {
			if !n.stacked() {
				c.Ends = append(c.Ends, ChartEnd{Pos: r.Pos, ID: r.ID, Because: n})
			}
		}
	}
	for _, r := range c.Rows {
		if r.Done || r.Ticket != nil && r.Ticket.Phases[0].State == StateDone {
			c.Started = true
		}
	}
	c.Ready = len(c.Briefs) > 0 || len(c.Run) > 0
	d.verdict(&c)
	return c
}

// rowBefore orders the rows: by wave (a cycle last), then rank (none last),
// then position.
func rowBefore(a, b ChartRow) bool {
	if (a.Wave < 0) != (b.Wave < 0) {
		return b.Wave < 0
	}
	if a.Wave != b.Wave {
		return a.Wave < b.Wave
	}
	switch {
	case a.Rank != nil && b.Rank == nil:
		return true
	case a.Rank == nil && b.Rank != nil:
		return false
	case a.Rank != nil && *a.Rank != *b.Rank:
		return *a.Rank < *b.Rank
	}
	return a.Pos < b.Pos
}

// chartDeriver holds the memoized walks over the entries.
type chartDeriver struct {
	f     ChartFacts
	byPos map[int]int
	byID  map[string]int

	reach []string
	why   []string
	state []int // 0 not seen, 1 being worked out, 2 known
	wave  []int
	wstat []int
}

// done says entry i is published or closed.
func (d *chartDeriver) done(i int) bool {
	t := d.f.Entries[i].Ticket
	return t != nil && (t.Code == CodeDone || t.Code == CodeClosed)
}

// target is the entry a need points at, when it is one of this chart.
func (d *chartDeriver) target(n ChartNeed) (int, bool) {
	if n.Ticket != "" {
		i, ok := d.byID[n.Ticket]
		return i, ok
	}
	i, ok := d.byPos[n.Pos]
	return i, ok
}

// label names entry i in a sentence: its ticket, or its place in the chart.
func (d *chartDeriver) label(i int) string {
	if id := d.f.Entries[i].ID; id != "" {
		return id
	}
	return fmt.Sprintf("entry #%d", d.f.Entries[i].Pos)
}

func reachRank(reach string) int {
	switch reach {
	case ReachBlocked:
		return 2
	case ReachAfterMerge:
		return 1
	}
	return 0
}

// reachOf is the reach of entry i. Precedence: blocked over after-merge over
// here, taken over the unmet needs.
func (d *chartDeriver) reachOf(i int) string {
	if d.state[i] == 2 {
		return d.reach[i]
	}
	d.state[i] = 1
	e := d.f.Entries[i]
	reach, why := ReachHere, ""
	switch {
	case d.done(i):
		reach = ReachDone
	case e.Problem != "" || e.ID != "" && e.Ticket == nil:
		reach, why = ReachBlocked, d.label(i)+" cannot be read: "+e.Problem
	default:
		for _, n := range e.Needs {
			if n.Met {
				continue
			}
			r, w := d.needReach(i, n)
			if reachRank(r) > reachRank(reach) {
				reach, why = r, w
			}
		}
	}
	d.reach[i], d.why[i], d.state[i] = reach, why, 2
	return reach
}

// needReach is what an unmet need of entry i does to its reach, and why when it
// blocks.
func (d *chartDeriver) needReach(i int, n ChartNeed) (string, string) {
	if n.Code == claimBlockerNotPlanned || n.Code == CodeNeedUnknown {
		return ReachBlocked, n.Why
	}
	ti, inChart := d.target(n)
	if !inChart {
		// Outside the chart nothing of this run builds it: a merged need
		// resumes once it merged, a stacked one never does.
		if n.stacked() {
			return ReachBlocked, n.Why
		}
		return ReachAfterMerge, ""
	}
	if d.state[ti] == 1 {
		return ReachBlocked, d.label(i) + " is on a cycle of blocked_by edges"
	}
	switch d.reachOf(ti) {
	case ReachBlocked:
		return ReachBlocked, fmt.Sprintf("%s waits for %s, which is blocked", d.label(i), d.label(ti))
	case ReachAfterMerge:
		return ReachAfterMerge, ""
	}
	// The target is done or builds here: a stacked edge is met by it in this
	// run, a merged one only once its pull request merges.
	if n.stacked() {
		return ReachHere, ""
	}
	return ReachAfterMerge, ""
}

// waveOf is the longest chain of in-chart blockers of entry i over every edge,
// -1 for a cycle or waiting on one.
func (d *chartDeriver) waveOf(i int) int {
	switch d.wstat[i] {
	case 1:
		return -1
	case 2:
		return d.wave[i]
	}
	d.wstat[i] = 1
	w := 0
	for _, n := range d.f.Entries[i].Needs {
		ti, ok := d.target(n)
		if !ok {
			continue
		}
		tw := d.waveOf(ti)
		switch {
		case tw < 0:
			w = -1
		case w >= 0 && tw+1 > w:
			w = tw + 1
		}
	}
	d.wave[i], d.wstat[i] = w, 2
	return w
}

// runWaves groups the graduated entries that build in this run (reachable here
// and not done) in waves over their unmet in-chart edges, in row order.
func (d *chartDeriver) runWaves(c *Chart) {
	here := map[int]bool{}
	for i, e := range d.f.Entries {
		if e.ID != "" && d.reach[i] == ReachHere {
			here[i] = true
		}
	}
	wave := map[int]int{}
	var waveOf func(i int) int
	waveOf = func(i int) int {
		if w, ok := wave[i]; ok {
			return w
		}
		w := 0
		for _, n := range d.f.Entries[i].Needs {
			if n.Met {
				continue
			}
			if ti, ok := d.target(n); ok && here[ti] && ti != i {
				if tw := waveOf(ti) + 1; tw > w {
					w = tw
				}
			}
		}
		wave[i] = w
		return w
	}
	for _, r := range c.Rows {
		i := d.byPos[r.Pos]
		if !here[i] {
			continue
		}
		w := waveOf(i)
		for len(c.Run) <= w {
			c.Run = append(c.Run, nil)
		}
		c.Run[w] = append(c.Run[w], r.ID)
	}
}

// verdict sets the chart's code, reason and prompt, the analog of a ticket's
// readiness.
func (d *chartDeriver) verdict(c *Chart) {
	graduated, done := 0, 0
	for _, r := range c.Rows {
		if r.ID == "" {
			continue
		}
		graduated++
		if r.Done {
			done++
		}
	}
	missing := entriesWithNoTicket(c.Ungraduated)
	switch {
	case c.Ready:
		c.Prompt = TakePrompt(c.Name)
		if c.Started {
			c.Prompt = ContinuePrompt(c.Name)
		}
		if c.Ungraduated > 0 {
			c.Reason = missing
		}
	case graduated == 0:
		c.Code, c.Reason = CodeChartNotGraduated, fmt.Sprintf("run `jig graduate %s`", c.Name)
	case done == graduated && c.Ungraduated > 0:
		c.Code, c.Reason = CodeChartNotGraduated, fmt.Sprintf("%s: run `jig graduate %s`", missing, c.Name)
	case done == graduated:
		c.Code = CodeChartDone
	case len(c.Ends) > 0:
		c.Code, c.Prompt = CodeChartWaitsMerge, ContinuePrompt(c.Name)
		c.Reason = d.waitsReason(c.Ends[0])
	default:
		c.Code = CodeChartBlocked
		for _, r := range c.Rows {
			if r.Reach == ReachBlocked && d.why[d.byPos[r.Pos]] != "" {
				c.Reason = d.why[d.byPos[r.Pos]]
				break
			}
		}
	}
}

// entriesWithNoTicket says how many entries wait for `jig graduate`.
func entriesWithNoTicket(n int) string {
	if n == 1 {
		return "1 entry has no ticket yet"
	}
	return fmt.Sprintf("%d entries have no ticket yet", n)
}

// waitsReason says what the run waits for at end: "SHOP-5 waits for SHOP-2 to
// merge (https://... OPEN)".
func (d *chartDeriver) waitsReason(end ChartEnd) string {
	blocker := end.Because.Ticket
	if blocker == "" {
		blocker = fmt.Sprintf("entry #%d", end.Because.Pos)
	}
	var prs []string
	for _, p := range end.Because.PRs {
		pr := p.URL
		if p.State != "" {
			pr += " " + p.State
		}
		prs = append(prs, pr)
	}
	out := fmt.Sprintf("%s waits for %s to merge", end.ID, blocker)
	if len(prs) > 0 {
		out += " (" + strings.Join(prs, ", ") + ")"
	}
	return out
}
