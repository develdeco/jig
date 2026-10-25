package tui

import (
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/events"
)

// buildCommits is the most commits of one slice the build screen lists; the
// ones before them are counted.
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
	p.add(m.st.bold("slices"))
	goals := t.goals()
	idW, stateW := 1, 5
	for _, s := range slices {
		idW = max(idW, width(s.ID))
		stateW = max(stateW, width(s.State))
	}
	for _, s := range slices {
		tries := strconv.Itoa(s.Attempts) + " " + plural(s.Attempts, "try", "tries")
		line := m.st.icon(s.State) + " " + pad(s.ID, idW) + "  " + m.st.state(s.State, pad(s.State, stateW)) +
			"  " + m.st.dim(pad(tries, 7)) + "  " + firstOf(goals[s.ID], m.st.dim("no goal"))
		line, below := m.withFacts(line, sliceFacts(s, true))
		p.addRow(rowRef{kind: rowSlice, id: s.ID, ticket: id, slice: s.ID}, line)
		m.sliceRecords(&p, t, s, idW, below)
	}

	if rows := m.sessionRows(t, roleBuilder); len(rows) > 0 {
		p.add("")
		p.add(m.st.bold("sessions"))
		for _, r := range rows {
			p.add(r)
		}
	}

	p.add("")
	m.activityPanel(&p, t, stepBuild)
	return p
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
	room := m.width - width(pre) - 4
	if facts != "" {
		p.add(pre + facts)
	}

	commits := t.commits(s.ID)
	if n := len(commits) - buildCommits; n > 0 {
		p.add(pre + m.st.dim("+"+strconv.Itoa(n)+" earlier "+plural(n, "commit", "commits")))
		commits = commits[n:]
	}
	failed, failedOK := t.lastRunFailed(s.ID)
	for _, c := range commits {
		line := m.st.icon(c.State) + " " + short(c.SHA)
		if c.Subject != "" {
			line += " “" + c.Subject + "”"
		}
		// The time is the end of the line, so the commit's words give way to it.
		when := m.commitWhen(c)
		p.add(pre + fit(line, room-width(when)-2) + m.st.dim("  "+when))
		for _, run := range c.Runs {
			state := StateGreen
			if run.Outcome != "pass" {
				state = StateStalled
			}
			p.add(pre + "  " + m.st.icon(state) + " " + m.st.dim(fit(oracleWords(run), room-4)))
			if failedOK && run.TS == failed.TS && run.Commit == failed.Commit {
				m.failureTail(p, pre+"    ", run, room-4)
			}
		}
		if c.Error != nil {
			p.add(pre + "  " + m.st.icon(StateStalled) + " " + m.st.dim(fit("could not be verified: "+firstOf(c.Error.Summary, "no reason recorded"), room-4)))
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
		p.add(pre + m.st.icon(state) + " " + fit(line, room))
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

// sessionRows are the sessions of the ticket that played role, one row each:
// the name the screens call it by, its harness and model, when it started
// and the start of its id, the one place a session's id is shown.
func (m *Model) sessionRows(t *ticket, role string) []string {
	ids := t.roleSessions()[role]
	nameW := 0
	for _, id := range ids {
		nameW = max(nameW, width(t.roleLabel(id, role)))
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		row := pad(t.roleLabel(id, role), nameW) + "  " + firstOf(m.sessionDesc(id), "harness unknown")
		if start, ok := m.sessionStart(t, id); ok {
			row += m.st.dim("  started " + clock(start, m.zone))
		}
		out = append(out, row+m.st.dim("  "+shortID(id)))
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
