package tui

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/events"
)

// buildCommits is the most commits of one slice the build screen's detail lists;
// the older ones are counted below them.
const buildCommits = 3

// buildPage is a ticket's build: a list of its slices, the detail of the slice
// the cursor is on, the sessions that worked on the ticket and the build's
// activity.
func (m *Model) buildPage(id string) page {
	var p page
	t := m.w.tickets[id]
	if t == nil {
		p.add(m.st.dim("no ticket " + id))
		return p
	}
	step := m.steps(t)[stepBuild]
	p.add(m.st.icon(step.State) + " " + m.st.state(step.State, step.Text))
	if line := m.fixBudgetLine(t); line != "" {
		p.add(line)
	}

	slices := t.slices(m.zone)
	if len(slices) == 0 {
		p.add("")
		p.add(m.st.dim("no slices yet"))
		if t.status == nil {
			p.add(m.st.dim("reading the ticket's status"))
		}
		return p
	}
	p.add("")
	goals := t.goals()
	work := m.clocksOf(t).work
	rows := make([]listRow, len(slices))
	green := 0
	for i, s := range slices {
		if s.State == StateGreen {
			green++
		}
		tries := strconv.Itoa(s.Attempts) + " " + plural(s.Attempts, "try", "tries")
		goal := firstOf(goals[s.ID], m.st.dim("no goal"))
		if round := t.fixRound(s.ID); round > 0 && goals[s.ID] != "" {
			// A fix slice reads as its finding, by the round it came from.
			goal = "round " + strconv.Itoa(round) + " · " + findingTitleFirst(goals[s.ID])
		}
		spent := ""
		if d := work.of(stepBuild, "slice:"+s.ID); d > 0 {
			spent = m.st.dim(span(d))
		}
		rows[i] = listRow{
			ref:   &rowRef{kind: rowSlice, id: s.ID, ticket: id, slice: s.ID},
			cells: []string{m.st.icon(s.State), s.ID, m.st.state(s.State, s.State), m.st.dim(tries), spent, goal},
		}
	}
	list := listSpec{title: "Slices", summary: strconv.Itoa(green) + " of " + strconv.Itoa(len(slices)) + " green", cols: sliceColumns, names: sliceColumnNames, rows: rows}
	at := m.cursorAmong(list)
	var full statusSlice
	if t.status != nil && at < len(t.status.Slices) {
		full = t.status.Slices[at]
	}
	detail := m.buildDetail(t, slices[at], full)
	m.listAndDetail(&p, &detail, at, list)

	if rows := m.sessionRows(t, roleBuilder); len(rows) > 0 {
		m.listPanel(&p, listSpec{title: "Sessions", summary: strconv.Itoa(len(rows)), cols: sessionColumns, names: sessionColumnNames, rows: rows})
	}

	m.stepActivityPanel(&p, t, stepBuild)
	return p
}

// findingTitleFirst is a finding's line, "dir/file.go:12 The title", with its path
// cut to the file before its title is: a row that has no room for all of it keeps
// the title.
func findingTitleFirst(line string) string {
	path, title, ok := strings.Cut(line, " ")
	if !ok || !strings.Contains(path, "/") {
		return line
	}
	return path[strings.LastIndex(path, "/")+1:] + " " + title
}

// cursorAmong is the row, among the rows of lists in order, that the Detail is of:
// the row the cursor of the focused list stands on, found by its identity as the
// panes find theirs, so a zoom, a search that narrows the rows or a row that comes
// in above leaves the Detail on its row. A focus on a pane that is not one of the
// lists leaves the Detail on the list that had it last.
func (m *Model) cursorAmong(lists ...listSpec) int {
	e := m.top()
	offs := make([]int, len(lists))
	n, k := 0, -1
	for i, l := range lists {
		offs[i] = n
		n += len(l.rows)
		if len(l.rows) > 0 && l.title == e.paneFocus {
			k = i
		}
	}
	if n == 0 {
		return 0
	}
	if k >= 0 {
		e.listFocus = e.paneFocus
	} else if i := slices.IndexFunc(lists, func(l listSpec) bool { return len(l.rows) > 0 && l.title == e.listFocus }); i >= 0 {
		k = i
	} else {
		k = slices.IndexFunc(lists, func(l listSpec) bool { return len(l.rows) > 0 })
	}
	ids := make([]rowIdentity, len(lists[k].rows))
	for i, r := range lists[k].rows {
		ids[i] = r.ref.identity()
	}
	// A pane with no cursor of its own yet has its first row.
	return offs[k] + m.paneCursorIn(lists[k].title, ids, 0)
}

// sliceColumns are the columns of a slice's row: its icon, id, state, tries, the
// time it took and goal; sliceColumnNames name them.
var (
	sliceColumns     = []column{{}, {id: true, gap: 1}, {gap: 2}, {gap: 2}, {gap: 2}, {free: true, gap: 2}}
	sliceColumnNames = []string{"", "slice", "state", "tries", "took", "goal"}
)

// sessionColumns are the columns of a session's row: its name, its harness and
// model, when it started and the start of its id.
var (
	sessionColumns     = []column{{}, {free: true, gap: 2}, {gap: 2}, {gap: 2}}
	sessionColumnNames = []string{"name", "harness", "started", "id"}
)

// dimmed is names drawn back, as the names of a list's columns are.
func dimmed(st styles, names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = st.dim(n)
	}
	return out
}

// fixBudgetLine is how much of the ticket's fix budget the rounds so far have
// used, "" before its first round. A budget that is reached is drawn forward:
// the next round parks what it finds for a person.
func (m *Model) fixBudgetLine(t *ticket) string {
	if t.status == nil || t.status.FixBudget == "" {
		return ""
	}
	text := "fix budget " + t.status.FixBudget
	if strings.HasSuffix(t.status.FixBudget, ", reached") {
		return "  " + m.st.state(StateFindings, text)
	}
	return "  " + m.st.dim(text)
}

// buildDetail is the detail of a slice: its goal and oracle whole, where it
// builds, what it waits for, the round it came from, the questions asked about
// it with their answers, and the commits that were checked with each run of the
// oracle against them and the output of a check that failed. The oracle is said
// once, and a run says its command only when it is another one.
func (m *Model) buildDetail(t *ticket, s sliceRow, full statusSlice) detailSpec {
	d := detailSpec{title: s.ID, summary: s.State}
	add := func(r detailRow) { d.rows = append(d.rows, r) }
	if full.Goal != "" {
		add(detailRow{label: "goal", text: full.Goal})
	}
	if s.Oracle != "" {
		add(detailRow{label: "oracle", text: s.Oracle})
	}
	if s.Workspace != "" {
		add(detailRow{label: "workspace", text: s.Workspace})
	}
	if s.BlockedBy != "" {
		add(detailRow{label: "waits for", text: strings.ReplaceAll(s.BlockedBy, "+", ", ")})
	}
	if round := t.fixRound(s.ID); round > 0 {
		from := strconv.Itoa(round)
		if len(full.Findings) > 0 {
			from += " · " + strings.Join(full.Findings, ", ")
		}
		add(detailRow{label: "round", text: from})
	}
	label := "questions"
	for _, q := range t.questions(m.zone) {
		if q.Slice != s.ID {
			continue
		}
		state := StateNeedsInput
		if !q.Open {
			state = StateOK
		}
		add(detailRow{label: label, icon: state, text: q.ID + " " + q.Text})
		label = ""
		if !q.Open && q.Answer != "" {
			add(detailRow{indent: 2, text: "→ " + q.Answer})
		}
	}
	m.commitRows(t, s, &d, "tries")
	if t.status != nil {
		if run, ok := t.status.LastRuns[s.ID]; ok && run.Result != "" {
			// What the status says of the slice's last witnessed run, in its own
			// words.
			state := StateGreen
			if run.Result != "pass" {
				state = StateStalled
			}
			add(detailRow{label: "last run", icon: state, dim: true, text: run.Result + " · covers: " + run.Covers})
		}
	}
	return d
}

// commitRows adds to d the commits of the slice that were checked, the newest
// first and the latest buildCommits of them, each with the runs of the oracle
// against it, the output of the run that failed and how its verification stands
// in phase's words with the command that clears it; the older ones are counted.
func (m *Model) commitRows(t *ticket, s sliceRow, d *detailSpec, label string) {
	add := func(r detailRow) { d.rows = append(d.rows, r) }
	commits := newestFirst(t.sliceCommits(s.ID), func(c commitRecord) time.Time { return c.Began })
	earlier := 0
	if n := len(commits) - buildCommits; n > 0 {
		earlier, commits = n, commits[:buildCommits]
	}
	failed, failedOK := t.lastRunFailed(s.ID)
	for _, c := range commits {
		// How the commit stands is the status's word: its verification state
		// as internal/phase tells it, with the command that clears it. A commit
		// the table has no row for is verified, as the journal says.
		row, hasRow := t.status.verificationOf(s.ID, c.SHA)
		icon := c.State
		switch {
		case t.status == nil:
			icon = StateQueued
		case hasRow:
			icon = row.icon()
		}
		line := short(c.SHA)
		if c.Subject != "" {
			line += " “" + c.Subject + "”"
		}
		if when := m.commitWhen(c); when != "" {
			line += "  " + when
		}
		add(detailRow{label: label, icon: icon, text: line})
		label = ""
		for _, run := range newestFirst(c.Runs, func(l events.Envelope) time.Time { at, _ := l.At(); return at }) {
			state := StateGreen
			switch {
			case run.Cause != "":
				// A run that judged nothing is not the code failing.
				state = StateQueued
			case run.Outcome != "pass":
				state = StateStalled
			}
			add(detailRow{indent: 2, icon: state, dim: true, text: m.runWords(t, run, s.Oracle)})
			if failedOK && run.TS == failed.TS && run.Commit == failed.Commit {
				m.failureRows(d, run)
			}
		}
		switch {
		case t.status == nil:
			add(detailRow{indent: 2, dim: true, text: readingText})
		case hasRow:
			// The state in phase's words, then the command that clears it.
			add(detailRow{indent: 2, icon: icon, dim: true, text: row.State})
			if row.Clear != "" {
				add(detailRow{indent: 4, dim: true, text: row.Clear})
			}
		}
	}
	if earlier > 0 {
		add(detailRow{dim: true, text: "+" + strconv.Itoa(earlier) + " earlier " + plural(earlier, "commit", "commits")})
	}
}

// failureOutput is the end of the output of a run that failed, as the oracle line
// recorded it ("" when it recorded none), and the note that says where the rest
// is, or where to read it when there is none. The line carries the last 600
// bytes of the output at most; the machine that ran the check has the rest in its
// verify log.
func failureOutput(run events.Envelope) (output, note string) {
	if output = firstOf(run.Summary, run.Signature); output == "" {
		return "", "no output was recorded: the verify log on the machine that ran it has it"
	}
	return output, "the end of its output; all of it is in the verify log on the machine that ran it"
}

// failureRows adds the end of the output of a run that failed to d.
func (m *Model) failureRows(d *detailSpec, run events.Envelope) {
	output, note := failureOutput(run)
	if output != "" {
		d.rows = append(d.rows, detailRow{indent: 4, state: StateStalled, text: output})
	}
	d.rows = append(d.rows, detailRow{indent: 4, dim: true, text: note})
}

// sessionRows are the sessions of the ticket that played role, a row each:
// the name the screens call it by, its harness and model, when it started
// and the start of its id, the one place a session's id is shown.
func (m *Model) sessionRows(t *ticket, role string) []listRow {
	ids := t.roleSessions()[role]
	out := make([]listRow, 0, len(ids))
	for _, id := range ids {
		started := ""
		if start, ok := m.sessionStart(t, id); ok {
			started = m.st.dim(clock(start, m.zone))
		}
		out = append(out, listRow{cells: []string{t.roleLabel(id, role), firstOf(m.sessionDesc(id), "harness unknown"), started, m.st.dim(shortID(id))}})
	}
	return out
}

// sessionStart is the ledger's session-start line of a session about the
// ticket, if the screens have seen it.
func (m *Model) sessionStart(t *ticket, session string) (events.Envelope, bool) {
	for _, l := range t.ledger {
		if l.Session == session && l.Event == "session-start" {
			return l, true
		}
	}
	return events.Envelope{}, false
}
