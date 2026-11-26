package tui

import (
	"slices"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/journal"
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

// findingStanding is how a finding stands now, as its screen says it: the state
// it is drawn in, a word, and a sentence for an ask. A finding the round recorded
// as an ask waits for a person only while the status lists it among the
// outstanding asks; once a person decided it, it says what was decided, and once
// a later round applied the decision, that too.
func (t *ticket) findingStanding(f statusFinding) (state, word, note string) {
	if f.Status != "asked" {
		return findingState(f), findingWord(f), ""
	}
	for _, a := range t.asks() {
		if a.ID != f.ID {
			continue
		}
		if a.Decision == "" {
			return StateAsk, "ask", "It waits for a person: decide it on the review screen's Asks list, with k to keep it and d to dismiss it. This screen decides nothing."
		}
		state, word = askDecision(a)
		return state, word, "It was an ask and it was " + word + "; the next round applies it. This screen decides nothing."
	}
	outcome, by := t.triageOf(f.ID)
	if outcome == "" {
		return StateOK, "decided", "It was an ask: a person decided it and a later round applied it."
	}
	state, word = askDecision(askRow{Decision: outcome, DecidedBy: by})
	return state, word, "It was an ask and it was " + word + "; a later round applied it."
}

// triageOf is what the journal's last triage line of the finding decided and who
// decided it, "" when none did.
func (t *ticket) triageOf(id string) (outcome, by string) {
	for _, l := range t.lines {
		if l.Event == journal.EventTriage && l.Ref == id {
			outcome, by = l.Outcome, l.Provenance
		}
	}
	return outcome, by
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
	state, word, note := t.findingStanding(*f)
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
	add("became", m.st.icon(state)+" "+m.st.state(state, word))
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
	if note != "" {
		p.boxAdd("")
		for _, l := range wrapWhole(note, m.innerWidth()) {
			p.boxAdd(m.st.state(state, l))
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

// roundNamed is the number of the round a row names, "" for a row that names none.
func roundNamed(ref rowRef) string {
	switch ref.kind {
	case rowRound, rowCarry:
		return ref.id
	case rowSub, rowLog:
		return ref.round
	}
	return ""
}

// goToRound puts the Rounds list's cursor on round n of the review screen on top
// and gives it the focus, so the Detail below is that round's.
func (m *Model) goToRound(n string) {
	if m.top().screen == screenReview {
		m.cursorToRow(func(r rowRef) bool { return r.kind == rowRound && r.id == n })
	}
}

// cursorToRow is the one way a screen is put on a row: the cursor of the screen
// on top stands on the first row match names, its pane has the focus, and the
// window is the one around it from the next frame, nothing after it moving it
// (the focus is the pane's already, so the first draw has none to move). A zoom
// or a search the screen was left with that hides the row is cleared first.
func (m *Model) cursorToRow(match func(rowRef) bool) {
	e := m.top()
	find := func() (page, int) {
		pg := m.page()
		for i, r := range pg.refs() {
			if match(r) {
				return pg, i
			}
		}
		return pg, -1
	}
	pg, i := find()
	if i < 0 && (e.zoom != "" || e.psearch.applied()) {
		e.zoom, e.psearch = "", paneSearch{}
		pg, i = find()
	}
	if i < 0 {
		return
	}
	refs := pg.refs()
	e.sel, e.follow, e.lead = i, false, 0
	e.seenSel, e.seenSet = i, true
	if at := paneOfRow(pg.panes(), i); at >= 0 {
		p := pg.panes()[at]
		e.paneFocus, e.textFocus = p.title, false
		e.rememberPane(pg, p, refs)
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
