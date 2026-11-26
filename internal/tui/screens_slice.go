package tui

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// timelineLines is how many of a slice's latest lines its screen shows.
const timelineLines = 40

// slicePage is one slice: its state and who holds it, where it builds and what
// checks it, the questions asked about it, and its lines newest first.
func (m *Model) slicePage(id, slice string) page {
	var p page
	t := m.w.tickets[id]
	if t == nil {
		p.add(m.st.dim("no ticket " + id))
		return p
	}
	var row *sliceRow
	for _, s := range t.slices(m.zone) {
		if s.ID == slice {
			s := s
			row = &s
		}
	}
	switch {
	case row == nil && t.status == nil:
		p.add(m.st.dim("reading the ticket's status"))
	case row == nil:
		p.add(m.st.dim("slice " + slice + " has no lines yet"))
	default:
		m.findingPanel(&p, t, *row)
		m.slicePanel(&p, t, *row)
	}

	if t.status != nil {
		var states []statusVerification
		for _, v := range t.status.Verifications {
			if v.Slice == slice {
				states = append(states, v)
			}
		}
		if len(states) > 0 {
			// Each commit that is not verified, in the status's words and with
			// the command that clears it.
			p.add("")
			// A row names its commit by a prefix; it stands where the commit's
			// first line puts it, and one the journal has no line of sinks.
			commits := t.commits(slice)
			began := func(v statusVerification) time.Time {
				for _, c := range commits {
					if v.Commit != "" && strings.HasPrefix(c.SHA, v.Commit) {
						return c.Began
					}
				}
				return time.Time{}
			}
			states = newestFirst(states, began)
			cells := make([][]string, len(states))
			for i, v := range states {
				cells[i] = []string{m.st.icon(v.icon()), short(v.Commit), m.st.dim(v.State)}
			}
			p.boxTop("Verification", strconv.Itoa(len(states)))
			laid := m.laidList(&p, verificationColumns, dimmed(m.st, verificationColumnNames), cells, 0)
			for i, v := range states {
				p.boxLaid(nil, laid[i])
				if v.Clear != "" {
					p.boxUnder("    " + m.st.dim(v.Clear))
				}
			}
			p.boxEnd()
		}
	}

	var qs []questionRow
	for _, q := range t.questions(m.zone) {
		if q.Slice == slice {
			qs = append(qs, q)
		}
	}
	if len(qs) > 0 {
		cells := make([][]string, len(qs))
		for i, q := range qs {
			state, word := StateNeedsInput, "open"
			if !q.Open {
				state, word = StateOK, "answered"
				if q.Provenance == "person" {
					word += " by you"
				}
			}
			asked := ""
			if q.Session != "" {
				asked = m.st.dim("asked by " + m.who(t, q.Session) + " at " + q.At)
			}
			cells[i] = []string{m.st.icon(state), q.ID, m.st.state(state, word), asked}
		}
		p.boxTop("Questions", strconv.Itoa(len(qs)))
		laid := m.laidList(&p, questionColumns, dimmed(m.st, questionColumnNames), cells, 0)
		for i, q := range qs {
			var ref *rowRef
			if q.Open {
				ref = &rowRef{kind: rowQuestion, id: q.ID, ticket: id, slice: slice}
			}
			p.boxLaid(ref, laid[i])
			for _, l := range m.panelLines(q.Text) {
				p.boxUnder("    " + l)
			}
			if !q.Open && q.Answer != "" {
				p.boxUnder("    " + m.st.state(StateOK, "answer: ") + q.Answer)
			}
		}
		p.boxEnd()
	}

	var lines []int
	for i, l := range t.lines {
		if l.Slice == slice {
			lines = append(lines, i)
		}
	}
	if len(lines) > 0 {
		lines = newestFirst(lines, func(i int) time.Time { at, _ := t.lines[i].At(); return at })
		older := 0
		if len(lines) > timelineLines {
			older, lines = len(lines)-timelineLines, lines[:timelineLines]
		}
		rows := make([]listRow, len(lines))
		for k, i := range lines {
			rows[k] = listRow{cells: m.timelineCells(t, t.lines[i])}
		}
		spec := listSpec{title: "Timeline", summary: strconv.Itoa(len(lines) + older), cols: timelineColumns, names: timelineColumnNames, rows: rows}
		if older > 0 {
			spec.after = []string{m.olderRow(older)}
		}
		m.listPanel(&p, spec)
	}
	return p
}

// The columns of the slice screen's lists: a commit's verification, a question,
// and a line of the timeline.
var (
	verificationColumns     = []column{{}, {gap: 1}, {free: true, gap: 2}}
	verificationColumnNames = []string{"", "commit", "state"}
	questionColumns         = []column{{}, {gap: 1}, {gap: 2}, {free: true, gap: 2}}
	questionColumnNames     = []string{"", "question", "state", "asked"}
	timelineColumns         = []column{{}, {free: true, gap: 1}}
	timelineColumnNames     = []string{"time", "what"}
)

// claimHolder is the session holding the slice by its latest claim line that
// no release has followed.
func claimHolder(t *ticket, slice string) (string, bool) {
	claim := t.latest(slice, journal.EventClaim)
	if claim < 0 || t.latest(slice, journal.EventRelease) > claim {
		return "", false
	}
	return t.lines[claim].Session, true
}

// factRow is a row of the slice's panel: a label, its value, and the slice the
// value names, if it names one.
type factRow struct {
	label, value string
	ref          *rowRef
}

// slicePanel adds the slice's own panel: its id, with what it stands at, and a
// grid of what is known of it, each field whole. A fact with nothing to say is
// not drawn.
func (m *Model) slicePanel(p *page, t *ticket, s sliceRow) {
	var rows []factRow
	add := func(label, value string) {
		if value != "" {
			rows = append(rows, factRow{label: label, value: value})
		}
	}
	full := t.statusSlice(s.ID)
	if t.fixRound(s.ID) == 0 {
		// A fix slice's goal is its finding, drawn above in a panel of its own.
		add("goal", strings.Join(strings.Fields(full.Goal), " "))
	}
	add("oracle", s.Oracle)
	add("state", m.st.icon(s.State)+" "+m.st.state(s.State, s.State))
	add("tries", strconv.Itoa(s.Attempts))
	if holder, ok := claimHolder(t, s.ID); ok {
		verb := "held by"
		if s.State == StateGreen {
			verb = "built by"
		}
		// A session that claimed the slice is its builder, whatever else it did
		// on the ticket.
		add(verb, firstOf(m.whoWithAs(t, holder, roleBuilder), "a session"))
	}
	all := t.slices(m.zone)
	named := func(label string, ids []string) {
		for i, id := range ids {
			state := ""
			for _, o := range all {
				if o.ID == id {
					state = o.State
				}
			}
			value := id
			if state != "" {
				value = m.st.icon(state) + " " + id
			}
			if i > 0 {
				label = ""
			}
			rows = append(rows, factRow{label: label, value: value, ref: &rowRef{kind: rowSlice, id: id, ticket: t.id, slice: id}})
		}
	}
	if s.BlockedBy != "" {
		named("after", strings.Split(s.BlockedBy, "+"))
	}
	var waiting []string
	for _, o := range all {
		if o.BlockedBy != "" && slices.Contains(strings.Split(o.BlockedBy, "+"), s.ID) {
			waiting = append(waiting, o.ID)
		}
	}
	named("needed by", waiting)
	add("workspace", s.Workspace)

	summary := m.st.icon(s.State) + " " + m.st.state(s.State, s.State) + " · " + strconv.Itoa(s.Attempts) + " " + plural(s.Attempts, "try", "tries")
	if d := m.clocksOf(t).work.of(stepBuild, "slice:"+s.ID); d > 0 {
		summary += " · " + span(d)
	}
	p.boxTop("Slice "+s.ID, summary)
	m.factRows(p, rows)
	p.boxEnd()
}

// factRows adds the rows of a grid of facts to the panel: a dim label column and
// the value after it, wrapped whole by the allocator.
func (m *Model) factRows(p *page, rows []factRow) {
	cells := make([][]string, len(rows))
	for i, r := range rows {
		cells[i] = []string{m.st.dim(r.label), r.value}
	}
	// A fact is never cut: its free text wraps as far as it goes.
	for i, lines := range layoutRows(runColumns, cells, m.innerWidth(), 1<<20) {
		p.boxLaid(rows[i].ref, lines)
	}
}

// findingPanel adds, for a fix slice, the panel that says why the slice exists:
// the round that sent it back, the findings it answers and the goal jig wrote
// for the builder, whole and as written, from its first line that is not a
// heading on. A slice that is no fix slice has none.
func (m *Model) findingPanel(p *page, t *ticket, s sliceRow) {
	round := t.fixRound(s.ID)
	if round == 0 {
		return
	}
	full := t.statusSlice(s.ID)
	p.boxTop("Finding", "round "+strconv.Itoa(round))
	if len(full.Findings) > 0 {
		m.factRows(p, []factRow{{label: "findings", value: strings.Join(full.Findings, ", ")}})
	}
	for _, l := range wrapWhole(goalBody(full.Goal), m.innerWidth()) {
		p.boxAdd(l)
	}
	p.boxEnd()
}

// goalBody is a goal from its first line that is not a heading (blank, or ending
// in a colon) on, its own line breaks kept.
func goalBody(goal string) string {
	lines := strings.Split(strings.ReplaceAll(goal, "\r\n", "\n"), "\n")
	for i, l := range lines {
		if l = strings.TrimSpace(l); l != "" && !strings.HasSuffix(l, ":") {
			return strings.Join(lines[i:], "\n")
		}
	}
	return ""
}

// statusSlice is the status's row of the slice, empty before the status is read.
func (t *ticket) statusSlice(id string) statusSlice {
	if t.status != nil {
		for _, s := range t.status.Slices {
			if s.ID == id {
				return s
			}
		}
	}
	return statusSlice{}
}
