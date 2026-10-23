package tui

import (
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/journal"
)

// timelineLines is how many of a slice's latest lines its screen shows.
const timelineLines = 40

// slicePage is one slice: its state and who holds it, the questions asked
// about it, and its lines in the order they were written.
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
	if row == nil {
		p.add(m.st.dim("slice " + slice + " has no lines yet"))
	} else {
		facts := []string{"state " + m.st.state(row.State, row.State), "try " + strconv.Itoa(row.Attempts)}
		if row.BlockedBy != "" {
			facts = append(facts, "blocked by "+row.BlockedBy)
		}
		if holder, ok := claimHolder(t, slice); ok {
			facts = append(facts, "held by "+firstOf(holder, "a session with no id"))
		}
		p.add(strings.Join(facts, m.st.dim(" · ")))
	}

	var qs []questionRow
	for _, q := range t.questions(m.zone) {
		if q.Slice == slice {
			qs = append(qs, q)
		}
	}
	if len(qs) > 0 {
		p.add("")
		p.add(m.st.bold("questions"))
		for _, q := range qs {
			state, word := StateNeedsInput, "open"
			if !q.Open {
				state, word = StateOK, "answered"
				if q.Provenance != "" {
					word += " by " + q.Provenance
				}
			}
			head := m.st.icon(state) + " " + q.ID + "  " + m.st.state(state, word)
			if q.Session != "" {
				head += m.st.dim("  asked by " + q.Session + " at " + q.At)
			}
			if q.Open {
				p.addRow(rowRef{kind: rowQuestion, id: q.ID, ticket: id, slice: slice}, head)
			} else {
				p.add("  " + head)
			}
			for _, l := range strings.Split(q.Text, "\n") {
				p.add("    " + l)
			}
			if !q.Open && q.Answer != "" {
				p.add("    " + m.st.state(StateOK, "answer: ") + q.Answer)
			}
		}
	}

	var lines []int
	for i, l := range t.lines {
		if l.Slice == slice {
			lines = append(lines, i)
		}
	}
	if len(lines) > 0 {
		p.add("")
		p.add(m.st.bold("timeline"))
		if len(lines) > timelineLines {
			lines = lines[len(lines)-timelineLines:]
		}
		for _, i := range lines {
			l := t.lines[i]
			p.add(m.st.dim(clock(l, m.zone)) + " " + m.st.icon(eventState(l)) + " " + describe(l))
		}
	}
	return p
}

// claimHolder is the session holding the slice by its latest claim line that
// no release has followed.
func claimHolder(t *ticket, slice string) (string, bool) {
	claim := t.latest(slice, journal.EventClaim)
	if claim < 0 || t.latest(slice, journal.EventRelease) > claim {
		return "", false
	}
	return t.lines[claim].Session, true
}
