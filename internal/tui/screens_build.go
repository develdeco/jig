package tui

import (
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/events"
)

// buildCommits is the most commits of one slice the build screen lists; the
// older ones are counted below them.
const buildCommits = 3

// buildPage is a ticket's build: each slice with its state, its goal, its
// workspace and oracle, what it waits for, the commits that were checked and
// how each check went, and the questions asked about it; the sessions that
// worked on the ticket; and the build's activity.
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
	idW, stateW, triesW := width(sliceColumnNames[1]), width(sliceColumnNames[2]), width(sliceColumnNames[3])
	cells := make([][]string, len(slices))
	for i, s := range slices {
		tries := strconv.Itoa(s.Attempts) + " " + plural(s.Attempts, "try", "tries")
		idW, stateW, triesW = max(idW, width(s.ID)), max(stateW, width(s.State)), max(triesW, width(tries))
		cells[i] = []string{m.st.icon(s.State), s.ID, m.st.state(s.State, s.State), m.st.dim(tries), firstOf(goals[s.ID], m.st.dim("no goal"))}
	}
	// A slice's facts end its row when they fit in the room its goal has, and are
	// a line under it when they do not.
	room := m.innerWidth() - (1 + 1 + idW + 2 + stateW + 2 + triesW + 2)
	below := make([]string, len(slices))
	for i, s := range slices {
		facts := sliceFacts(s, true)
		switch {
		case facts == "":
		case width(cells[i][4])+2+width(facts) <= room:
			cells[i][4] += m.st.dim("  " + facts)
		default:
			below[i] = m.st.dim(facts)
		}
	}
	p.boxTop("Slices", strconv.Itoa(len(slices)))
	laid := m.laidList(&p, sliceColumns, dimmed(m.st, sliceColumnNames), cells, 0)
	for i, s := range slices {
		p.boxLaid(&rowRef{kind: rowSlice, id: s.ID, ticket: id, slice: s.ID}, laid[i])
		at := len(p.lines)
		m.sliceRecords(&p, t, s, idW, below[i])
		for k := at; k < len(p.lines); k++ {
			p.lines[k].box, p.lines[k].under = boxMid, true
		}
	}
	p.boxEnd()

	if rows := m.sessionRows(t, roleBuilder); len(rows) > 0 {
		m.listPanel(&p, listSpec{title: "Sessions", summary: strconv.Itoa(len(rows)), cols: sessionColumns, names: sessionColumnNames, rows: rows})
	}

	m.stepActivityPanel(&p, t, stepBuild)
	return p
}

// sliceColumns are the columns of a slice's row: its icon, id, state, tries and
// goal; sliceColumnNames name them.
var (
	sliceColumns     = []column{{}, {id: true, gap: 1}, {gap: 2}, {gap: 2}, {free: true, gap: 2}}
	sliceColumnNames = []string{"", "slice", "state", "tries", "goal"}
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

// sliceRecords adds, under a slice's row, what is on record of it: where it
// builds and what checks it, the commits that were checked with each run of the
// oracle against them, the output of a check that failed, and the questions
// asked about it with their answers. idW is the width of the ids' column, and
// facts what the row had no room for, drawn back.
func (m *Model) sliceRecords(p *page, t *ticket, s sliceRow, idW int, facts string) {
	pre := "    " + spaces(idW)
	room := m.innerWidth() - width(pre) - 2
	if facts != "" {
		p.add(pre + facts)
	}

	commits := newestFirst(t.commits(s.ID), func(c commitRecord) time.Time { return c.Began })
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
		line := m.st.icon(icon) + " " + short(c.SHA)
		if c.Subject != "" {
			line += " “" + c.Subject + "”"
		}
		// The time is the end of the line, so the commit's words give way to it.
		when := m.commitWhen(c)
		p.add(pre + line + m.st.dim("  "+when))
		for _, run := range newestFirst(c.Runs, func(l events.Envelope) time.Time { at, _ := l.At(); return at }) {
			state := StateGreen
			switch {
			case run.Cause != "":
				// A run that judged nothing is not the code failing.
				state = StateQueued
			case run.Outcome != "pass":
				state = StateStalled
			}
			p.add(pre + "  " + m.st.icon(state) + " " + m.st.dim(oracleWords(run)))
			if failedOK && run.TS == failed.TS && run.Commit == failed.Commit {
				m.failureTail(p, pre+"    ", run, room-4)
			}
		}
		switch {
		case t.status == nil:
			p.add(pre + "  " + m.st.dim(readingText))
		case hasRow:
			// The state in phase's words, then the command that clears it.
			for i, l := range wrap(row.State, room-4, 2) {
				lead := "  " + m.st.icon(icon) + " "
				if i > 0 {
					lead = "    "
				}
				p.add(pre + lead + m.st.dim(l))
			}
			for _, l := range wrap(row.Clear, room-4, 2) {
				p.add(pre + "    " + m.st.dim(l))
			}
		}
	}

	if earlier > 0 {
		p.add(pre + m.st.dim("+"+strconv.Itoa(earlier)+" earlier "+plural(earlier, "commit", "commits")))
	}

	if t.status != nil {
		if run, ok := t.status.LastRuns[s.ID]; ok && run.Result != "" {
			// What the status says of the slice's last witnessed run, in its
			// own words.
			state := StateGreen
			if run.Result != "pass" {
				state = StateStalled
			}
			p.add(pre + m.st.icon(state) + " " + m.st.dim("last run: "+run.Result+" · covers: "+run.Covers))
		}
	}

	for _, q := range t.questions(m.zone) {
		if q.Slice != s.ID {
			continue
		}
		state, line := StateNeedsInput, q.ID+" "+firstLine(q.Text)
		if !q.Open {
			state = StateOK
			if q.Answer != "" {
				line += " → " + firstLine(q.Answer)
			}
		}
		p.add(pre + m.st.icon(state) + " " + line)
	}
}

// commitWhen is when a commit's latest word came: its verification, or the run
// or the request that is the latest thing on record about it.
func (m *Model) commitWhen(c commitRecord) string {
	var last events.Envelope
	switch {
	case c.State == StateGreen:
		last = c.Verified
	case c.Error != nil:
		last = *c.Error
	case len(c.Runs) > 0:
		last = c.Runs[len(c.Runs)-1]
	default:
		last = c.At
	}
	if last.TS == "" {
		return ""
	}
	return clock(last, m.zone)
}

// failureTail adds the end of the output of a run that failed, as the oracle line
// recorded it, or where to read it when the line recorded none. The line
// carries the last 600 bytes of the output at most; the machine that ran the
// check has the rest in its verify log.
func (m *Model) failureTail(p *page, pre string, run events.Envelope, room int) {
	tail := firstOf(run.Summary, run.Signature)
	if tail == "" {
		for _, l := range wrap("no output was recorded: the verify log on the machine that ran it has it", room, 2) {
			p.add(pre + m.st.dim(l))
		}
		return
	}
	for _, l := range wrap(tail, room, 3) {
		p.add(pre + m.st.state(StateStalled, l))
	}
	for _, l := range wrap("the end of its output; all of it is in the verify log on the machine that ran it", room, 2) {
		p.add(pre + m.st.dim(l))
	}
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
