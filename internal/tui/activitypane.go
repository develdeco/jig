package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/events"
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
		refs[i] = logRef(t.id, e.l, e.key, e.step, "", seen)
	}
	return refs
}

// logRef is the row of the line l of ticket, whose place is the step and the
// sub-step key key (step -1 for none). A line is told from the others by its
// instant, source, event, session, slice and subject, and from a line that is the
// same in all of these by its order, which seen counts; prefix puts something
// before them when the rows of several tickets stand together.
func logRef(ticket string, l events.Envelope, key string, step int, prefix string, seen map[string]int) rowRef {
	id := prefix + strings.Join([]string{l.TS, l.Src, l.Event, l.Session, l.Slice, l.Ref}, "|")
	k := seen[id]
	seen[id]++
	if k > 0 {
		id += "#" + strconv.Itoa(k)
	}
	r := rowRef{kind: rowLog, id: id, ticket: ticket, slice: l.Slice}
	if l.Event == journal.EventQuestion || l.Event == journal.EventAnswer {
		r.question = l.Ref
	}
	if s, ok := strings.CutPrefix(key, "slice:"); ok {
		r.slice = s
	}
	if n, ok := strings.CutPrefix(key, "round:"); ok {
		r.round = n
	}
	if step >= 0 {
		r.container = strings.ToLower(stepNames[step])
	}
	return r
}

// stepActivityPanel adds the activity of step to p as a pane, for the step's own
// screen, newest first: a row for every line of the step, of which it draws the
// window around the cursor that the rows left below what p holds allow,
// activityLines at most and three at least, so the newest line and the panel's
// edge stay on the screen. Each line names the sub-step it advances and opens
// what it is about, as the ticket screen's does.
func (m *Model) stepActivityPanel(p *page, t *ticket, step int) {
	all := newestFirst(t.stepLines(step), func(l events.Envelope) time.Time { at, _ := l.At(); return at })
	title := stepNames[step] + " activity"
	room := min(activityCap(m.bodyRows()), max(3, m.bodyRows()-m.taken(p)-3))
	if r, ok := m.zoomRoom(title); ok {
		room = r
	}
	seen := make(map[string]int, len(all))
	refs := make([]rowRef, len(all))
	ids := make([]rowIdentity, len(all))
	for i, l := range all {
		refs[i] = logRef(t.id, l, t.subKey(l), step, "", seen)
		ids[i] = refs[i].identity()
	}
	if p.windowed == nil {
		p.windowed = map[string][]rowRef{}
	}
	p.windowed[title] = refs
	cur := m.paneCursorIn(title, ids, 0)
	from, to, merged := windowAround(len(all), cur, cur+1, 0, room)
	right := ""
	switch {
	case to-from == len(all):
	case from == 0:
		right = fmt.Sprintf("latest %d of %d", to, len(all))
	default:
		right = fmt.Sprintf("%d-%d of %d", from+1, to, len(all))
	}
	labels := m.subLabelsOf(t)
	rows := make([]listRow, 0, to-from)
	for i := from; i < to; i++ {
		rows = append(rows, listRow{ref: &refs[i], cells: m.activityCells(t, labels, all[i])})
	}
	spec := listSpec{title: title, summary: right, cols: activityColumns, names: activityColumnNames, rows: rows, empty: "nothing yet",
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

// chartActivityPanel adds the activity of the chart to p as a pane, newest first:
// a row for every line of every ticket, each with the ticket and the step it
// belongs to, and for every commit of the chart's folder in the store (history,
// newest first), of which it draws the window around its cursor that the rows
// below what p holds allow, as the ticket screen's does. A line opens its
// ticket; a commit opens nothing.
func (m *Model) chartActivityPanel(p *page, rows []chartRow, history []chartCommit) {
	var all []chartLine
	for _, r := range rows {
		if r.t == nil {
			continue
		}
		for _, pl := range m.placed(r.t, r.d) {
			at, _ := pl.l.At()
			all = append(all, chartLine{id: r.id, t: r.t, placedLine: pl, at: at})
		}
	}
	// The commits come after the tickets' lines, oldest first, so that at one
	// instant the stable sort keeps git's order (the newer above the older) and
	// a commit stands above a ticket's line.
	for i := len(history) - 1; i >= 0; i-- {
		all = append(all, chartLine{id: chartCommitID, commit: &history[i], at: history[i].Time})
	}
	all = newestFirst(all, func(c chartLine) time.Time { return c.at })
	// The edges and the names take three of the rows the panel has.
	room := max(activityFloor-3, m.bodyRows()-m.taken(p)-3)
	if r, ok := m.zoomRoom("Activity"); ok {
		room = r
	}
	refs := m.chartLogRefs(all)
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
	items := make([]listRow, 0, to-from)
	for i := from; i < to; i++ {
		e := all[i]
		tag := e.tag()
		if e.commit != nil {
			items = append(items, listRow{ref: &refs[i], cells: []string{m.st.dim(clockAt(e.at, m.zone)), e.id, m.st.dim(tag), m.st.dim("· "+short(e.commit.SHA)) + " " + e.commit.Subject}})
			continue
		}
		if e.step >= 0 {
			tag = m.st.state(stepTagState[e.step], tag)
		} else {
			tag = m.st.dim(tag)
		}
		items = append(items, listRow{ref: &refs[i], cells: []string{m.st.dim(clock(e.l, m.zone)), e.id, tag, m.moveText(e.t, e.l)}})
	}
	spec := listSpec{title: "Activity", summary: right, cols: chartActivityColumns, names: chartActivityColumnNames, rows: items, empty: "nothing yet",
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

// chartLogRefs are the rows of a chart's activity lines: a ticket's line is a row
// of its ticket, which enter opens and the keys act on, told from the ticket's
// other lines by its place in the log (the container); a commit is a row of its
// own that opens nothing.
func (m *Model) chartLogRefs(all []chartLine) []rowRef {
	seen := make(map[string]int, len(all))
	refs := make([]rowRef, len(all))
	for i, e := range all {
		var id string
		if e.commit != nil {
			id = "commit|" + e.commit.SHA
		} else {
			l := e.l
			id = strings.Join([]string{e.id, l.TS, l.Src, l.Event, l.Session, l.Slice, l.Ref}, "|")
		}
		k := seen[id]
		seen[id]++
		if k > 0 {
			id += "#" + strconv.Itoa(k)
		}
		if e.commit != nil {
			refs[i] = rowRef{kind: rowCommit, id: id}
			continue
		}
		refs[i] = rowRef{kind: rowTicket, id: e.id, ticket: e.id, container: id}
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
