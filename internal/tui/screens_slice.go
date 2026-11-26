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
	if g := t.goals()[slice]; g != "" {
		p.add(m.st.bold(g))
	}
	if row == nil && t.status == nil {
		p.add(m.st.dim("reading the ticket's status"))
	} else if row == nil {
		p.add(m.st.dim("slice " + slice + " has no lines yet"))
	} else {
		facts := []string{m.st.icon(row.State) + " " + m.st.state(row.State, row.State), strconv.Itoa(row.Attempts) + " " + plural(row.Attempts, "try", "tries")}
		if row.BlockedBy != "" {
			facts = append(facts, "after slice "+strings.ReplaceAll(row.BlockedBy, "+", " and "))
		}
		if holder, ok := claimHolder(t, slice); ok {
			verb := "held by "
			if row.State == StateGreen {
				verb = "built by "
			}
			facts = append(facts, verb+firstOf(m.whoWith(t, holder), "a session"))
		}
		p.add(strings.Join(facts, m.st.dim(" · ")))
		if where := sliceFacts(*row, false); where != "" {
			p.add(m.st.dim(where))
		}
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

	// The sections of the brief that cite the slice: each a row that opens the
	// brief at it.
	if t.status != nil && t.status.Brief != nil {
		var rows []listRow
		for i, s := range t.status.Brief.Sections {
			if !slices.Contains(s.Slices, slice) {
				continue
			}
			changed := ""
			if s.Since == sinceChangedWord {
				changed = m.st.state(StateFindings, "changed since the approval")
			}
			rows = append(rows, listRow{ref: &rowRef{kind: rowSection, id: sectionID(i), ticket: id}, cells: []string{s.Heading, changed}})
		}
		if len(rows) > 0 {
			m.listPanel(&p, listSpec{title: "Brief sections", summary: strconv.Itoa(len(rows)), cols: briefSectionColumns, names: briefSectionColumnNames, rows: rows})
		}
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
	briefSectionColumns     = []column{{free: true}, {gap: 2}}
	briefSectionColumnNames = []string{"section", ""}
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
