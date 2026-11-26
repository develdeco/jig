package tui

import (
	"slices"
	"strconv"
	"strings"
)

// A finding's screen: one finding a gate round raised, whole and wrapped, and
// each fix slice it became as a row that opens the slice. The screen decides
// nothing: a finding that waits for a person is decided on the review screen's
// Asks list, where `k` and `d` act.

// fixSliceColumns are the columns of the finding screen's fix slice rows.
var (
	fixSliceColumns     = []column{{}, {id: true, gap: 1}, {gap: 2}, {free: true, gap: 2}}
	fixSliceColumnNames = []string{"", "slice", "state", "goal"}
)

// findingNamed is the status's row of the finding id, nil when it has none.
func (t *ticket) findingNamed(id string) *statusFinding {
	if t == nil || t.status == nil {
		return nil
	}
	for i := range t.status.Findings {
		if t.status.Findings[i].ID == id {
			return &t.status.Findings[i]
		}
	}
	return nil
}

// fixSlicesOf are the slices whose findings column names the finding, in plan order.
func (t *ticket) fixSlicesOf(id string) []statusSlice {
	var out []statusSlice
	if t.status == nil {
		return out
	}
	for _, s := range t.status.Slices {
		if slices.Contains(s.Findings, id) {
			out = append(out, s)
		}
	}
	return out
}

// findingPage is the finding's screen, top to bottom: the finding's own panel
// (where it is, what the reviewer wrote, how it stands and who sorted it) and the
// fix slices it became.
func (m *Model) findingPage(id, finding string) page {
	var p page
	t := m.w.tickets[id]
	if t == nil {
		p.add(m.st.dim("no ticket " + id))
		return p
	}
	f := t.findingNamed(finding)
	switch {
	case f == nil && t.status == nil:
		p.add(m.st.dim("reading the ticket's status"))
		return p
	case f == nil:
		p.add(m.st.dim("finding " + finding + " is not in the status"))
		return p
	}
	state := findingState(*f)
	var rows []factRow
	add := func(label, value string) {
		if value != "" {
			rows = append(rows, factRow{label: label, value: value})
		}
	}
	add("where", f.Where)
	add("title", f.Title)
	add("risk", f.Risk)
	add("why", f.Rationale)
	add("action", f.Action)
	add("became", m.st.icon(state)+" "+m.st.state(state, findingWord(*f)))
	if f.Triage != "" {
		add("sorted by", map[string]string{"human": "a person", "auto": "the gate"}[f.Triage])
	}
	add("round", strconv.Itoa(f.Round))
	p.boxTop("Finding "+f.ID, "round "+strconv.Itoa(f.Round)+" · "+firstOf(f.Risk, "risk unknown"))
	m.factRows(&p, rows)
	if strings.TrimSpace(f.Detail) != "" {
		p.boxAdd("")
		for _, l := range wrapWhole(f.Detail, m.innerWidth()) {
			p.boxAdd(l)
		}
	}
	if f.Status == "asked" {
		p.boxAdd("")
		for _, l := range wrapWhole("It waits for a person: decide it on the review screen's Asks list, with k to keep it and d to dismiss it. This screen decides nothing.", m.innerWidth()) {
			p.boxAdd(m.st.state(StateAsk, l))
		}
	}
	p.boxEnd()

	fixes := t.fixSlicesOf(f.ID)
	states := map[string]string{}
	for _, s := range t.slices(m.zone) {
		states[s.ID] = s.State
	}
	list := listSpec{title: "Fix slices", summary: strconv.Itoa(len(fixes)), cols: fixSliceColumns, names: fixSliceColumnNames, room: -1}
	for _, s := range fixes {
		st := states[s.ID]
		list.rows = append(list.rows, listRow{
			ref:   &rowRef{kind: rowSlice, id: s.ID, ticket: t.id, slice: s.ID, container: f.ID},
			cells: []string{m.st.icon(st), s.ID, m.st.state(st, st), goalLine(s.Goal)},
		})
	}
	if len(fixes) == 0 {
		list.empty = "no fix slice was queued for this finding"
	}
	m.listPanel(&p, list)
	return p
}

// goToRound puts the Rounds list's cursor on round n of the review screen on top
// and gives it the focus, so the Detail below is that round's.
func (m *Model) goToRound(n string) {
	e := m.top()
	if e.screen != screenReview {
		return
	}
	pg := m.page()
	for i, r := range pg.refs() {
		if r.kind == rowRound && r.id == n {
			e.sel, e.seenSet, e.follow, e.lead = i, false, false, 0
			m.syncPanes(pg)
			return
		}
	}
}

// findingSibs are the findings of the round the finding on top is of, in the
// order the round's detail lists them.
func (m *Model) findingSibs(e *navEntry) *sibSet {
	t := m.w.tickets[e.ticket]
	f := t.findingNamed(e.finding)
	if f == nil {
		return nil
	}
	s := &sibSet{kind: sibFindings, list: len(m.nav) - 1}
	for _, o := range t.status.Findings {
		if o.Round == f.Round {
			s.ids = append(s.ids, o.ID)
		}
	}
	s.at = slices.Index(s.ids, e.finding)
	return s
}

// roundSibs are the rounds of the ticket on the review screen on top, oldest
// first, and the one the Rounds cursor stands on; nil when the ticket has fewer
// than two, which have nothing to step between.
func (m *Model) roundSibs(e *navEntry) *sibSet {
	t := m.w.tickets[e.ticket]
	if t == nil {
		return nil
	}
	rounds := withStatusRounds(t, t.rounds(m.zone))
	if len(rounds) < 2 {
		return nil
	}
	s := &sibSet{kind: sibRounds, list: len(m.nav) - 1}
	for _, r := range rounds {
		s.ids = append(s.ids, strconv.Itoa(r.N))
	}
	slices.SortFunc(s.ids, func(a, b string) int { return atoi(a) - atoi(b) })
	// The Rounds cursor, else the newest round, which the list opens on.
	cur := s.ids[len(s.ids)-1]
	if pc := e.paneCurs["Rounds"]; pc.set && pc.id.kind == rowRound {
		cur = pc.id.id
	}
	s.at = slices.Index(s.ids, cur)
	return s
}
