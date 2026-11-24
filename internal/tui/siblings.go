package tui

import (
	"slices"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
)

// Siblings: the screens of one kind that stand next to the one on top. A
// slice's are its ticket's slices in plan order. A ticket's (its own screen and
// every step of it) are the ticket rows of the list it was opened from, read
// through the page that draws that list. A chart's are the chart rows of the
// project's list.

// sibKind is what the siblings are.
type sibKind int

const (
	sibSlices sibKind = iota + 1
	sibTickets
	sibCharts
)

// noun is one sibling, in the words a reason says it.
func (k sibKind) noun() string {
	switch k {
	case sibSlices:
		return "slice"
	case sibCharts:
		return "chart"
	}
	return "ticket"
}

// sibSet is the siblings of the screen on top and its place among them.
type sibSet struct {
	kind sibKind
	ids  []string
	// at is the screen's place among ids, -1 when it is no longer there.
	at int
	// list is the place in the stack of the list the siblings are rows of, and
	// refs the rows of its page: the cursor of the list is put on the neighbor.
	list int
	refs []rowRef
	// rowAt is the place in refs of the row each of ids stands on.
	rowAt []int
}

// sibs are the siblings of the screen on top, nil when it has none. A frame
// reads them once: Update and View clear what was read.
func (m *Model) sibs() *sibSet {
	if !m.sibRead {
		m.sibMemo, m.sibRead = m.readSibs(), true
	}
	return m.sibMemo
}

// forgetSibs drops what sibs read, so the next ask reads the screens as they are.
func (m *Model) forgetSibs() { m.sibMemo, m.sibRead = nil, false }

func (m *Model) readSibs() *sibSet {
	e := m.top()
	switch e.screen {
	case screenSlice:
		t := m.w.tickets[e.ticket]
		if t == nil || t.status == nil {
			return nil
		}
		s := &sibSet{kind: sibSlices, list: len(m.nav) - 1}
		for _, sl := range t.status.Slices {
			s.ids = append(s.ids, sl.ID)
		}
		s.at = slices.Index(s.ids, e.slice)
		return s
	case screenTicket, screenBrief, screenBuild, screenReview, screenPublish, screenLand:
		// The ticket's own screen is the lowest of the run of its screens; the
		// list is the screen under it.
		i := len(m.nav) - 1
		for i > 0 && m.nav[i-1].ticket == e.ticket {
			i--
		}
		if i < 1 {
			return nil
		}
		return m.listSibs(sibTickets, i-1, e.ticket, func(r rowRef) string {
			if r.kind == rowTicket {
				return r.id
			}
			return ""
		})
	case screenChart:
		return m.listSibs(sibCharts, 0, e.chart, func(r rowRef) string {
			switch {
			case r.kind == rowChart:
				return r.id
			case r.kind == rowTicket:
				// An issue's row opens its plan.
				return m.planOf(r.id)
			}
			return ""
		})
	}
	return nil
}

// listSibs are the siblings read from the rows of the list at place list of the
// stack: of each row, pick names the sibling it stands for, "" for none, and the
// first row of a sibling is where the list's cursor goes.
func (m *Model) listSibs(kind sibKind, list int, own string, pick func(rowRef) string) *sibSet {
	s := &sibSet{kind: kind, list: list, refs: m.refsOf(list)}
	for i, r := range s.refs {
		if id := pick(r); id != "" && !slices.Contains(s.ids, id) {
			s.ids, s.rowAt = append(s.ids, id), append(s.rowAt, i)
		}
	}
	if len(s.ids) == 0 {
		return nil
	}
	s.at = slices.Index(s.ids, own)
	return s
}

// refsOf are the rows of the list at place j of the stack. The project's list
// is read from the page it keeps (mainRefs), so a frame of a ticket builds no
// list; any other list is built as the screen has it on top, the stack cut to it
// for the read, and counted as the page build it is.
func (m *Model) refsOf(j int) []rowRef {
	if j == 0 {
		return m.mainRefs()
	}
	saved := m.nav
	m.nav = m.nav[: j+1 : j+1]
	refs := m.page().refs()
	m.nav = saved
	return refs
}

// place is the screen's place among its siblings, "(2 of 5)", "" when it has
// none.
func (m *Model) place() string {
	if s := m.sibs(); s != nil && s.at >= 0 && len(s.ids) > 1 {
		return " (" + strconv.Itoa(s.at+1) + " of " + strconv.Itoa(len(s.ids)) + ")"
	}
	return ""
}

// siblingNoun is what the siblings of the screen on top are, in a word.
func (m *Model) siblingNoun() string {
	if s := m.sibs(); s != nil {
		return s.kind.noun()
	}
	return "sibling"
}

// canSibling says whether the sibling delta places from the screen exists, and
// when it does not, why.
func (m *Model) canSibling(delta int) (bool, string) {
	s := m.sibs()
	switch {
	case s == nil:
		return false, "this screen has no siblings"
	case s.at < 0:
		return false, m.top().subjectName() + " is in no list of this screen"
	case s.at+delta < 0:
		return false, "the first " + s.kind.noun()
	case s.at+delta >= len(s.ids):
		return false, "the last " + s.kind.noun()
	}
	return true, ""
}

// subjectName is the screen's subject as a reason says it.
func (e *navEntry) subjectName() string {
	switch e.screen {
	case screenSlice:
		return "slice " + e.slice
	case screenChart:
		return e.chart
	}
	return e.ticket
}

// openSibling is `[` and `]`: the neighbor opens as the screen the person is
// on, replacing the screens above its list, and the list puts its cursor on it.
func (m *Model) openSibling(delta int) tea.Cmd {
	s := m.sibs()
	if ok, _ := m.canSibling(delta); !ok {
		return nil
	}
	e := m.top()
	id := s.ids[s.at+delta]
	m.forgetSibs()
	if s.kind == sibSlices {
		return m.goTo(len(m.nav)-1, nil, navEntry{screen: screenSlice, ticket: e.ticket, slice: id})
	}
	// The list's cursor is on the neighbor, which esc lands on.
	list := &m.nav[s.list]
	i := s.rowAt[s.at+delta]
	list.sel, list.follow, list.lead = i, false, 0
	if list.screen == screenMain {
		list.cur, list.curSet = s.refs[i].identity(), true
	}
	if s.kind == sibCharts {
		return m.goTo(s.list+1, nil, navEntry{screen: screenChart, chart: id, follow: true})
	}
	if chart := m.planOf(id); chart != "" {
		// An issue's screen is its plan, as enter on its row opens it.
		return m.goTo(s.list+1, nil, navEntry{screen: screenChart, chart: chart, follow: true})
	}
	entries := []navEntry{{screen: screenTicket, ticket: id, follow: true}}
	if e.screen != screenTicket {
		if t := m.w.tickets[id]; t == nil || !isMulti(t) {
			entries = append(entries, navEntry{screen: e.screen, ticket: id})
		}
	}
	return m.goTo(s.list+1, nil, entries...)
}
