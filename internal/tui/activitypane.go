package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// activityPanel adds the ticket's activity log to p as a pane, newest first: a
// row for every line, of which it draws the window around its cursor that the
// rows left below what p holds allow, three at least, so the panel's edge stays
// on the screen and a tall one is filled to its foot. It counts the lines past
// the window on each side.
func (m *Model) activityPanel(p *page, t *ticket) {
	all := newestFirst(m.placed(t, m.phaseOf(t)), func(e placedLine) time.Time { at, _ := e.l.At(); return at })
	// The edges and the names take three of the rows the panel has.
	room := max(3, m.bodyRows()-m.taken(p)-3)
	if r, ok := m.zoomRoom("Activity"); ok {
		room = r
	}
	refs := m.logRefs(t, all)
	ids := make([]rowIdentity, len(refs))
	for i, r := range refs {
		ids[i] = r.identity()
	}
	if p.windowed == nil {
		p.windowed = map[string][]rowRef{}
	}
	p.windowed["Activity"] = refs
	cur := m.paneCursorIn("Activity", ids, 0)
	from, to, merged := windowAround(len(all), cur, cur+1, 0, room)
	right := ""
	switch {
	case to-from == len(all):
	case from == 0:
		right = fmt.Sprintf("latest %d of %d", to, len(all))
	default:
		right = fmt.Sprintf("%d-%d of %d", from+1, to, len(all))
	}
	rows := make([]listRow, 0, to-from)
	for i := from; i < to; i++ {
		e := all[i]
		tag := tagOf(e.step, e.key)
		if e.step >= 0 {
			tag = m.st.state(stepTagState[e.step], tag)
		} else {
			tag = m.st.dim(tag)
		}
		rows = append(rows, listRow{ref: &refs[i], cells: []string{m.st.dim(clock(e.l, m.zone)), tag, m.moveText(t, e.l)}})
	}
	spec := listSpec{title: "Activity", summary: right, cols: activityColumns, names: activityColumnNames, rows: rows, empty: "nothing yet",
		room: room - markLines(from, to, len(all), merged)}
	switch {
	case merged:
		spec.after = []string{m.st.dim(fmt.Sprintf("↑ %d newer · ↓ %d older", from, len(all)-to))}
	default:
		if from > 0 {
			spec.before = []string{m.st.dim(fmt.Sprintf("↑ %d newer", from))}
		}
		if to < len(all) {
			spec.after = []string{m.olderRow(len(all) - to)}
		}
	}
	m.listPanel(p, spec)
}

// logRefs are the rows of the log's lines, newest first: each is told from the
// others by its instant, source, event, session, slice and subject, and carries
// what the line is about, which is what enter opens (openTarget).
func (m *Model) logRefs(t *ticket, all []placedLine) []rowRef {
	seen := make(map[string]int, len(all))
	refs := make([]rowRef, len(all))
	for i, e := range all {
		l := e.l
		id := strings.Join([]string{l.TS, l.Src, l.Event, l.Session, l.Slice, l.Ref}, "|")
		k := seen[id]
		seen[id]++
		if k > 0 {
			// Lines that are the same in every one of these keep their order.
			id += "#" + strconv.Itoa(k)
		}
		r := rowRef{kind: rowLog, id: id, ticket: t.id, slice: l.Slice}
		if l.Event == journal.EventQuestion || l.Event == journal.EventAnswer {
			r.question = l.Ref
		}
		if s, ok := strings.CutPrefix(e.key, "slice:"); ok {
			r.slice = s
		}
		if n, ok := strings.CutPrefix(e.key, "round:"); ok {
			r.round = n
		}
		if e.step >= 0 {
			r.container = strings.ToLower(stepNames[e.step])
		}
		refs[i] = r
	}
	return refs
}

// promptFor is the id of the open question whose answer prompt the row opens: a
// question in Needs you, or a line of the log that asks or answers it, while it
// is open; "" for any other row, and for a question that is answered.
func (m *Model) promptFor(ref rowRef) string {
	qid := ref.question
	if ref.kind == rowQuestion {
		qid = ref.id
	}
	if qid == "" {
		return ""
	}
	if t := m.w.tickets[ref.ticket]; t != nil {
		for _, q := range t.questions(m.zone) {
			if q.ID == qid && q.Open {
				return qid
			}
		}
	}
	return ""
}
