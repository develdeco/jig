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
	// CodeChartDone: every ticket of the chart is published or closed, and none
	// of the published ones is stacked on a parent whose pull request is open.
	CodeChartDone = "CHART_DONE"
	// CodeChartWaitsMerge: nothing can start in this run and a pull request
	// has to merge first (so has the pull request of a parent that a published
	// ticket is stacked on, which owes it a retarget); "Continue <chart>"
	// resumes once it has.
	CodeChartWaitsMerge = "CHART_WAITS_MERGE"
	// CodeChartBlocked: nothing can start and no merge frees it.
	CodeChartBlocked = "CHART_BLOCKED"
	// CodeChartNotGraduated: the chart has entries with no ticket yet and
	// nothing else to run: `jig graduate <chart>` mints them (`jig validate
	// <chart>` first while one has a problem). A chart with no entries at all
	// has it too: the entries are still to be written.
	CodeChartNotGraduated = "CHART_NOT_GRADUATED"
)

// CodeNeedUnknown is the code of a need whose blocker is no ticket of the store
// (as a claim would find no ticket to read): it can never be met.
const CodeNeedUnknown = "CHART_BLOCKER_UNKNOWN"

// CodeNeedNotNamed is the code of a need of a named ticket on a ticket of the
// chart that the run does not take (the prompt did not name it) and that is not
// done: the run does not build it, so the need waits for the person to name it
// or take it first. It is also the verdict's code when it is all the run waits
// for.
const CodeNeedNotNamed = "CHART_BLOCKER_NOT_NAMED"

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
	// published and they are recorded. For a stacked need of a published ticket
	// they are the parent's pull requests that have not merged: the merge of one
	// owes the ticket a retarget, so the ticket is done and still waits on it
	// (Why says so).
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
	// Named are the ids of the tickets the person named: the run is over those
	// only (see scopeNamed). Empty means the whole chart.
	Named []string
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

// ChartEnd is where a run ends: row Pos waits on Because, a merged edge, or the
// stacked edge of a published ticket whose parent's pull request is open (the
// ticket is done until that merges, and owed a retarget after).
type ChartEnd struct {
	Pos     int
	ID      string
	Because ChartNeed
}

// Chart is where a chart stands.
type Chart struct {
	Name string
	// Named are the ids the run was named over, in row order; nil for the whole
	// chart as no one named it.
	Named []string
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
	// edge (not through another entry), with the pull requests it waits on, and
	// the published entries stacked on a parent whose pull request is open. The
	// chart is not done while there is one: the parent's merge makes the entry's
	// branch owed a retarget, a change that nothing else in the chart announces.
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

// TakeTicketsPrompt is what to ask to take several named tickets of a chart to
// their pull requests: the ids where TakePrompt has the chart's name.
func TakeTicketsPrompt(ids []string) string { return "Take " + andJoin(ids) + " to PR" }

// ContinueTicketsPrompt is what to ask to resume the tickets a run was named
// over.
func ContinueTicketsPrompt(ids []string) string { return "Continue " + andJoin(ids) }

// andJoin joins words as prose: "a", "a and b", "a, b and c".
func andJoin(words []string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

// scopeNamed narrows facts to the named tickets. An unnamed entry is no row and
// is outside the run, as an entry outside the chart is. A need of a named entry
// on an unnamed one that is not done (or has no ticket yet) cannot be met by
// this run: it takes CodeNeedNotNamed. A need on an unnamed one that is done
// reads as a need outside the chart does.
func scopeNamed(f ChartFacts) ChartFacts {
	named := map[string]bool{}
	for _, id := range f.Named {
		named[id] = true
	}
	doneOf := func(e ChartEntry) bool {
		return e.Ticket != nil && (e.Ticket.Code == CodeDone || e.Ticket.Code == CodeClosed)
	}
	byID, byPos := map[string]ChartEntry{}, map[int]ChartEntry{}
	for _, e := range f.Entries {
		byPos[e.Pos] = e
		if e.ID != "" {
			byID[e.ID] = e
		}
	}
	out := ChartFacts{Name: f.Name, Named: f.Named}
	for _, e := range f.Entries {
		if e.ID == "" || !named[e.ID] {
			continue
		}
		needs := make([]ChartNeed, len(e.Needs))
		copy(needs, e.Needs)
		for i, n := range needs {
			if n.Met || n.Code == claimBlockerNotPlanned || n.Code == CodeNeedUnknown {
				continue
			}
			var target ChartEntry
			var ok bool
			blocker := n.Ticket
			if n.Ticket != "" {
				target, ok = byID[n.Ticket]
			} else {
				target, ok = byPos[n.Pos]
				blocker = fmt.Sprintf("entry #%d", n.Pos)
			}
			if !ok || named[target.ID] || doneOf(target) {
				continue
			}
			needs[i].Code = CodeNeedNotNamed
			needs[i].Why = fmt.Sprintf("%s waits for %s, which this run does not take and is not done", e.ID, blocker)
			needs[i].PRs = nil
		}
		e.Needs = needs
		out.Entries = append(out.Entries, e)
	}
	return out
}

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
	scoped := len(f.Named) > 0
	if scoped {
		f = scopeNamed(f)
	}
	d := &chartDeriver{
		scoped: scoped,
		f:      f,
		byPos:  map[int]int{},
		byID:   map[string]int{},
		reach:  make([]string, len(f.Entries)),
		why:    make([]string, len(f.Entries)),
		state:  make([]int, len(f.Entries)),
		wave:   make([]int, len(f.Entries)),
		wstat:  make([]int, len(f.Entries)),
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
		if r.ID == "" {
			continue
		}
		if r.Done {
			c.Ends = append(c.Ends, d.retargetEnds(r)...)
			continue
		}
		if r.Ticket != nil && r.Ticket.Current == Brief && (r.Reach == ReachHere || r.Reach == ReachAfterMerge) {
			c.Briefs = append(c.Briefs, r.ID)
		}
		if r.Reach != ReachAfterMerge {
			continue
		}
		for _, n := range r.Waits {
			if !n.stacked() || n.Code == CodeNeedNotNamed {
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
	if scoped {
		for _, r := range c.Rows {
			c.Named = append(c.Named, r.ID)
		}
	}
	d.verdict(&c)
	return c
}

// retargetEnds are the ends of done row r: a published ticket (a closed one
// owes nothing) stacked on a parent whose pull request is still open. A stacked
// edge is met by the parent's clean gate round, so the row is done now; once the
// parent merges the ticket is owed a retarget (ADR 0038) and the chart is ready
// again. The reader names the open pull requests only on such a need, so a
// stacked need that has none is no end.
func (d *chartDeriver) retargetEnds(r ChartRow) []ChartEnd {
	if r.Ticket == nil || r.Ticket.Code != CodeDone {
		return nil
	}
	var out []ChartEnd
	for _, n := range d.f.Entries[d.byPos[r.Pos]].Needs {
		if n.stacked() && len(n.PRs) > 0 {
			out = append(out, ChartEnd{Pos: r.Pos, ID: r.ID, Because: n})
		}
	}
	return out
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
	// scoped says the facts were narrowed to the named tickets.
	scoped bool
	f      ChartFacts
	byPos  map[int]int
	byID   map[string]int

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
	case e.ID == "" && e.Problem != "":
		// An entry with no ticket yet has a problem in the chart's own file (a
		// ref that does not resolve), not an unreadable ticket.
		reach, why = ReachBlocked, d.label(i)+" has a problem: "+e.Problem
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
	if n.Code == CodeNeedNotNamed {
		// The run does not build the blocker, whatever the edge's kind.
		return ReachAfterMerge, ""
	}
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
		c.Prompt = d.prompt(c, c.Started)
		if c.Ungraduated > 0 {
			c.Reason = missing
		}
	case len(c.Rows) == 0:
		// A chart just made: there is nothing to graduate, which graduate
		// refuses, so the next step is to add entries and check them.
		c.Code, c.Reason = CodeChartNotGraduated, fmt.Sprintf("the chart has no entries yet: add them to charts/%s/tickets.yaml and check them with `jig validate %s`", c.Name, c.Name)
	case graduated == 0:
		c.Code, c.Reason = CodeChartNotGraduated, c.graduateStep()
	case done == graduated && c.Ungraduated > 0:
		c.Code, c.Reason = CodeChartNotGraduated, missing+": "+c.graduateStep()
	case done == graduated && len(c.Ends) == 0:
		c.Code = CodeChartDone
	case len(c.Ends) > 0:
		root := c.RootEnds()
		end, onlyNotNamed := root[0], true
		for _, e := range root {
			if e.Because.Code != CodeNeedNotNamed {
				if onlyNotNamed {
					end = e
				}
				onlyNotNamed = false
			}
		}
		if onlyNotNamed {
			// A is not built by this run, so no merge frees it: name it too.
			c.Code, c.Prompt = CodeNeedNotNamed, d.prompt(c, c.Started)
		} else {
			c.Code, c.Prompt = CodeChartWaitsMerge, d.prompt(c, true)
		}
		c.Reason = d.waitsReason(end)
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

// RootEnds are the ends a run waits on first. An end whose blocker is a row of
// this run that is itself after a merge follows from that row's own end: "C
// waits for B to merge" is nothing to act on while B waits for a ticket the run
// does not take, or for a merge of its own. Every end, when none is a root.
func (c Chart) RootEnds() []ChartEnd {
	waits := map[string]bool{}
	for _, r := range c.Rows {
		if !r.Done && r.Reach == ReachAfterMerge {
			waits[endKey(r.ID, r.Pos)] = true
		}
	}
	var root []ChartEnd
	for _, e := range c.Ends {
		if !waits[endKey(e.Because.Ticket, e.Because.Pos)] {
			root = append(root, e)
		}
	}
	if len(root) == 0 {
		return c.Ends
	}
	return root
}

// endKey names a row or a need's blocker: its ticket, else its place.
func endKey(id string, pos int) string {
	if id != "" {
		return id
	}
	return fmt.Sprintf("#%d", pos)
}

// prompt is what to ask for the run: the chart's, the tickets' it was named
// over (two or more), or, for a single named ticket (not a chart run), that
// ticket's own prompt.
func (d *chartDeriver) prompt(c *Chart, resume bool) string {
	if !d.scoped {
		if resume {
			return ContinuePrompt(c.Name)
		}
		return TakePrompt(c.Name)
	}
	var ids []string
	for _, r := range c.Rows {
		ids = append(ids, r.ID)
	}
	switch {
	case len(ids) == 1:
		if t := c.Rows[0].Ticket; t != nil {
			return Prompt(t.Current, ids[0])
		}
		return ""
	case len(ids) == 0:
		return ""
	case resume:
		return ContinueTicketsPrompt(ids)
	}
	return TakeTicketsPrompt(ids)
}

// HasUngraduatedProblem says an entry with no ticket yet has a problem in the
// chart's own file: `jig graduate` would refuse it, so `jig validate <chart>`
// is the step that lists what is wrong.
func (c Chart) HasUngraduatedProblem() bool {
	for _, r := range c.Rows {
		if r.ID == "" && r.Problem != "" {
			return true
		}
	}
	return false
}

// graduateStep is the step that mints the entries with no ticket: `jig
// graduate`, or, while one of them has a problem, `jig validate` first.
func (c Chart) graduateStep() string {
	if c.HasUngraduatedProblem() {
		return fmt.Sprintf("run `jig validate %s` first: it lists the problems of the entries that have no ticket yet", c.Name)
	}
	return fmt.Sprintf("run `jig graduate %s`", c.Name)
}

// entriesWithNoTicket says how many entries wait for `jig graduate`.
func entriesWithNoTicket(n int) string {
	if n == 1 {
		return "1 entry has no ticket yet"
	}
	return fmt.Sprintf("%d entries have no ticket yet", n)
}

// waitsReason says what the run waits for at end: "SHOP-5 waits for SHOP-2 to
// merge (https://... OPEN)", and for a stacked end, which owes a retarget once
// the parent has merged, "SHOP-5 waits for SHOP-2 to merge (https://... OPEN),
// then its pull request is retargeted".
func (d *chartDeriver) waitsReason(end ChartEnd) string {
	if end.Because.Code == CodeNeedNotNamed {
		return end.Because.Why
	}
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
	if end.Because.stacked() {
		out += ", then its pull request is retargeted"
	}
	return out
}
