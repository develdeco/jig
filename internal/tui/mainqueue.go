package tui

import (
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/land"
)

// The project's list is a queue of what the person does next: every top-level
// row is in the first group whose rule holds for it, the groups are drawn in the
// order below, and a group with no rows is not drawn. The rules read what the
// chart screen reads (chartRow.needs, the digest, the end states), so a ticket
// is in the same place on both.

// mainGroup is one group of the project's list.
type mainGroup int

const (
	// groupNeeds are the rows that wait for the person: an open question, an
	// undecided ask, a brief waiting for approval, or a landing.
	groupNeeds mainGroup = iota
	// groupRunning are the rows a session is at work on, or whose current step is
	// under way.
	groupRunning
	// groupReady are the rows that can start, with nothing unmet.
	groupReady
	// groupWaiting are the rows with an unmet blocker, owing a merge, or that
	// must land after another ticket.
	groupWaiting
	// groupCharts are the charts that plan no issue.
	groupCharts
	// groupNotStarted are the rows no work began on, that are not ready.
	groupNotStarted
	// groupDone are the rows at their end, and the tickets the roster does not
	// list.
	groupDone
	groupCount
)

var groupNames = [groupCount]string{"needs you", "running", "ready", "waiting", "charts", "not started", "done"}

// countWords are the groups as the header counts them.
var countWords = [groupCount]string{"need you", "running", "ready", "waiting", "charts", "not started", "done"}

func (g mainGroup) name() string { return groupNames[g] }

// folds is whether the group is one fold row until the person opens it.
func (g mainGroup) folds() bool { return g == groupNotStarted || g == groupDone }

// heading is the group's name with the number of rows it holds.
func (g mainGroup) heading(n int) string { return groupNames[g] + " (" + strconv.Itoa(n) + ")" }

// queueRow is a top-level row of the project's list: a ticket, or a chart that
// plans no issue.
type queueRow struct {
	id    string
	chart bool
	group mainGroup
	// t, steps and cur are the ticket's, and its current step.
	t     *ticket
	steps []stepRow
	cur   stepRow
	// at is the latest line written about the row, the order of the running
	// group; next is the row's place in the order to start the ready ones, 0 for
	// a ticket the roster's next list does not name.
	at   time.Time
	next int
	// key is the id the row is ordered by in the groups ordered by id: the
	// row's own, or the best among a container's and its tasks'.
	key string
	// container is the issue or multi-task the row is drawn under, "" for a
	// top-level row; depth how far in it is drawn; kids the rows drawn under
	// it, in the chart's order.
	container string
	depth     int
	kids      []queueRow
}

// ref is what the cursor stands on when it is on the row.
func (r queueRow) ref() rowRef {
	if r.chart {
		return rowRef{kind: rowChart, id: r.id}
	}
	return rowRef{kind: rowTicket, id: r.id, ticket: r.id, container: r.container}
}

// tasks counts the rows drawn under the row, at any depth.
func (r queueRow) tasks() int {
	n := len(r.kids)
	for _, k := range r.kids {
		n += k.tasks()
	}
	return n
}

// mainQueue is the project's list, grouped.
type mainQueue struct {
	rows    []queueRow
	byGroup [groupCount][]queueRow
	// idW and titleW are the columns of the ticket rows; chartIDW and
	// chartTitleW the chart rows'. They hold for every row, folded or not, so a
	// fold opened moves nothing.
	idW, titleW, chartIDW, chartTitleW int
	// issues counts the issues among the rows, drawn anywhere.
	issues int
	// byChart says the list is laid out by chart: sections are the rows that head
	// the tickets of a chart (an issue's row or a chart's), and byGroup holds
	// only the tickets in no chart.
	byChart  bool
	sections []queueRow
}

// mainRows reads the roster's rows into the groups: the roster is all main
// draws, so a ticket it does not list is no row.
func (m *Model) mainRows() mainQueue {
	var q mainQueue
	q.idW = 4
	rows := make(map[string]queueRow, len(m.w.order))
	for _, id := range m.w.order {
		r := m.ticketRow(id)
		rows[id] = r
		q.idW = max(q.idW, width(id))
		q.titleW = max(q.titleW, width(r.t.title))
		if isIssue(r.t) {
			q.issues++
		}
	}
	// A task a container draws is no row of its own, and a task two issues list
	// is drawn under each.
	drawn := map[string]bool{}
	built := map[string]queueRow{}
	for _, id := range m.w.order {
		if t := rows[id].t; isIssue(t) || isMulti(t) {
			c := m.containerRow(rows[id], rows, drawn, 0, map[string]bool{id: true})
			built[id] = c
			q.idW = max(q.idW, c.indentedIDW())
		}
	}
	for _, id := range m.w.order {
		if drawn[id] {
			continue
		}
		if c, ok := built[id]; ok {
			q.rows = append(q.rows, c)
		} else {
			q.rows = append(q.rows, rows[id])
		}
	}
	q.titleW = min(q.titleW, max(0, (m.width-q.idW-20)/3))
	var names []string
	for _, name := range m.w.chartOrder {
		if c := m.w.charts[name]; c != nil && c.listing.Issue == "" {
			names = append(names, name)
			q.rows = append(q.rows, m.chartRow(name, c))
		}
	}
	if len(names) > 0 {
		q.chartIDW, q.chartTitleW = m.chartColumns(names, q.idW, q.titleW)
	}
	if m.nav[0].byChart {
		m.chartView(&q, rows, built, drawn)
		return q
	}
	for _, r := range q.rows {
		q.byGroup[r.group] = append(q.byGroup[r.group], r)
	}
	for g := range q.byGroup {
		sortGroup(mainGroup(g), q.byGroup[g])
	}
	return q
}

// chartView lays the list out by chart: each chart that plans an issue is the
// issue's row, each other chart its own, every one heading the tickets it lists
// in the store's order of charts; the tickets in no chart come last, in the
// groups of the view by state with done folded. A ticket two charts list is
// under each.
func (m *Model) chartView(q *mainQueue, rows, built map[string]queueRow, drawn map[string]bool) {
	q.byChart = true
	inChart := map[string]bool{}
	for _, name := range m.w.chartOrder {
		c := m.w.charts[name]
		if c == nil {
			continue
		}
		for _, id := range c.ids() {
			inChart[id] = true
		}
		if issue := c.listing.Issue; issue != "" {
			if row, ok := built[issue]; ok {
				inChart[issue] = true
				q.sections = append(q.sections, row)
			}
			continue
		}
		head := m.chartRow(name, c)
		for _, id := range c.ids() {
			r, ok := rows[id]
			if !ok {
				continue
			}
			if b, ok := built[id]; ok {
				r = b
			}
			head.kids = append(head.kids, under(r, name, 1))
		}
		q.sections = append(q.sections, head)
	}
	for _, r := range q.rows {
		if !r.chart && !inChart[r.id] {
			q.byGroup[r.group] = append(q.byGroup[r.group], r)
		}
	}
	for g := range q.byGroup {
		sortGroup(mainGroup(g), q.byGroup[g])
	}
	for _, s := range q.sections {
		q.idW = max(q.idW, s.indentedIDW())
	}
	q.titleW = min(q.titleW, max(0, (m.width-q.idW-20)/3))
}

// rowsIn counts the rows of the groups.
func (q mainQueue) rowsIn() int {
	n := 0
	for _, rows := range q.byGroup {
		n += len(rows)
	}
	return n
}

// under is the row drawn under the container, depth levels in, with the rows
// under it as far further in.
func under(r queueRow, container string, depth int) queueRow {
	shift := depth - r.depth
	r.container = container
	return shifted(r, shift)
}

func shifted(r queueRow, by int) queueRow {
	r.depth += by
	kids := make([]queueRow, len(r.kids))
	for i, k := range r.kids {
		kids[i] = shifted(k, by)
	}
	r.kids = kids
	return r
}

// ticketRow is the row of ticket id and the group the first rule that holds
// puts it in.
func (m *Model) ticketRow(id string) queueRow {
	r := m.rowOf(id, "", nil)
	t := r.t
	if t.digest == nil {
		// A ticket the roster gave no digest to stands as its status says.
		r.steps = m.stepWords(t)
	}
	row := queueRow{id: id, key: id, t: t, steps: r.steps, cur: r.steps[currentStep(r.steps)], at: t.lastActivity(), next: t.readyAt}
	d := t.digest
	if isIssue(t) || isMulti(t) {
		row.group = m.containerGroup(t, r.needs)
		if st, _ := m.planLanding(id, t); st == StateWaitsToLand || st == StateLanded || st == StateClosed {
			// The container is drawn as its own landing stands, in the end
			// states' icons.
			row.cur = stepRow{State: st}
		}
		return row
	}
	unmet := len(t.blockers) > 0 || r.behind || t.state == "blocked" || (d != nil && len(d.LandFirst) > 0)
	switch {
	case r.needs:
		row.group = groupNeeds
	case m.ticketLive(t) || (t.state != "new" && !r.atEnd && !row.cur.Done && row.cur.State != StateQueued && row.cur.State != StateReady):
		// A step that is not done and is not waiting to start is under way, a
		// stalled or failed one too: its builder has not finished with it. A
		// ticket the roster says is new has no work begun, its brief being
		// drafted or not, unless a session is at work on it.
		row.group = groupRunning
	case !unmet && t.state != "new" && ((d != nil && d.Ready) || (d == nil && t.state == "ready")):
		// A new ticket's digest says its brief can start; it is not started.
		row.group = groupReady
	case unmet:
		row.group = groupWaiting
	case r.atEnd:
		row.group = groupDone
	default:
		row.group = groupNotStarted
	}
	return row
}

// isIssue says t is an issue.
func isIssue(t *ticket) bool {
	return t != nil && t.digest != nil && t.digest.Kind == kindIssue
}

// containerGroup is the group an issue or a multi-task stands in by itself, as
// a ticket would: its own landing waits for the person, it is running, its pull
// request is ready, or it waits for its tasks; landed or closed it is done.
func (m *Model) containerGroup(t *ticket, needs bool) mainGroup {
	st, _ := m.planLanding(t.id, t)
	switch {
	case st == StateLanded || st == StateClosed:
		return groupDone
	case needs || st == StateWaitsToLand:
		return groupNeeds
	case m.ticketLive(t):
		return groupRunning
	case t.digest.State == land.StateReady:
		return groupReady
	}
	return groupWaiting
}

// taskIDs are the tickets a container draws under it, in the chart's order: the
// chart that plans an issue lists them, a multi-task's digest names its own.
// An entry with no ticket yet is none.
func (m *Model) taskIDs(t *ticket) []string {
	if isIssue(t) {
		if name := m.planOf(t.id); name != "" {
			if c := m.w.charts[name]; c != nil {
				return c.ids()
			}
		}
	}
	var ids []string
	for _, c := range t.digest.Children {
		if c.ID != "" {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

// containerRow is the row of an issue or a multi-task with its tasks under it:
// the rows of the tasks it lists that the roster has, a multi-task's own tasks
// under the multi-task. The container is in the first group that holds for it
// or any task, and takes the best key among them; landed or closed it is done
// whatever its tasks say. seen holds the containers on the way down.
func (m *Model) containerRow(own queueRow, rows map[string]queueRow, drawn map[string]bool, depth int, seen map[string]bool) queueRow {
	own.depth = depth
	ids := m.taskIDs(own.t)
	for _, id := range ids {
		r, ok := rows[id]
		if !ok || seen[id] {
			continue
		}
		if parent := m.multiParent(id); parent != "" && parent != own.id && slices.Contains(ids, parent) {
			// A multi-task's task is drawn under the multi-task.
			continue
		}
		drawn[id] = true
		if m.shared(r.t) && own.t.status != nil && own.t.status.Branch != "" {
			// A task two issues list stands here as its pull request and landing
			// into this issue make of it, once the issue's branch is known.
			cut := m.rowInPlan(m.rowOf(id, "", nil), own.id, own.t.status.Branch)
			r.t, r.steps, r.cur = cut.t, cut.steps, cut.steps[currentStep(cut.steps)]
		}
		r.container, r.depth = own.id, depth+1
		if isMulti(r.t) {
			next := maps.Clone(seen)
			next[id] = true
			r = m.containerRow(r, rows, drawn, depth+1, next)
			r.container = own.id
		}
		own.kids = append(own.kids, r)
	}
	if st, _ := m.planLanding(own.id, own.t); st == StateLanded || st == StateClosed {
		return own
	}
	for _, k := range own.kids {
		own.group = min(own.group, k.group)
		if k.at.After(own.at) {
			own.at = k.at
		}
		if k.next != 0 && (own.next == 0 || k.next < own.next) {
			own.next = k.next
		}
		if naturalLess(k.key, own.key) {
			own.key = k.key
		}
	}
	return own
}

// indentedIDW is the width the row's id and its tasks' ids need with their
// indentation.
func (r queueRow) indentedIDW() int {
	w := width(r.id) + 2*r.depth
	for _, k := range r.kids {
		w = max(w, k.indentedIDW())
	}
	return w
}

// chartRow is the row of a chart that plans no issue. It is in Needs you when
// one of its tickets waits for the person or its landing does, Running when a
// session is at work on it, Done when its run is, and the Charts group
// otherwise.
func (m *Model) chartRow(name string, c *chart) queueRow {
	row := queueRow{id: name, key: name, chart: true, group: groupCharts}
	state, _ := m.chartStatus(c, m.chartRows(c))
	_, live := c.live(m.w, m.now())
	for _, id := range c.ids() {
		if t := m.w.tickets[id]; t != nil {
			if at := t.lastActivity(); at.After(row.at) {
				row.at = at
			}
		}
	}
	switch {
	case state == StateNeedsInput:
		row.group = groupNeeds
	case live:
		row.group = groupRunning
	case state == StatePublished:
		row.group = groupDone
	}
	return row
}

// lastActivity is when the latest line about the ticket was written, journal
// or ledger; zero when it has none.
func (t *ticket) lastActivity() time.Time {
	var best time.Time
	for _, lines := range [][]events.Envelope{t.lines, t.ledger} {
		if n := len(lines); n > 0 {
			if at, ok := lines[n-1].At(); ok && at.After(best) {
				best = at
			}
		}
	}
	return best
}

// sortGroup orders a group's rows: running by latest activity, newest first;
// ready in the order `jig next` lists them to start, the ones it does not list
// after; every other group by id, numbers in number order.
func sortGroup(g mainGroup, rows []queueRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch g {
		case groupRunning:
			if !a.at.Equal(b.at) {
				return a.at.After(b.at)
			}
		case groupReady:
			if a.next != b.next && a.next != 0 && b.next != 0 {
				return a.next < b.next
			}
			if (a.next == 0) != (b.next == 0) {
				return b.next == 0
			}
		}
		return naturalLess(a.key, b.key)
	})
}

// naturalLess orders ids with their numbers as numbers: T-22 before T-23, and
// T-9 before T-10.
func naturalLess(a, b string) bool {
	for a != "" && b != "" {
		ta, tb := idRun(a), idRun(b)
		a, b = a[len(ta):], b[len(tb):]
		if ta == tb {
			continue
		}
		if startsWithDigit(ta) && startsWithDigit(tb) {
			na, nb := strings.TrimLeft(ta, "0"), strings.TrimLeft(tb, "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			return ta < tb
		}
		return ta < tb
	}
	return len(a) < len(b)
}

// idRun is the leading run of digits of s, or of what is not a digit.
func idRun(s string) string {
	digit := s[0] >= '0' && s[0] <= '9'
	i := 1
	for i < len(s) && (s[i] >= '0' && s[i] <= '9') == digit {
		i++
	}
	return s[:i]
}

func startsWithDigit(s string) bool { return s != "" && s[0] >= '0' && s[0] <= '9' }

// queueSummary is the header's count of the groups: "2 need you · 1 running ·
// 89 done", the empty ones left out.
func (m *Model) queueSummary() string {
	if m.readingStore() {
		return ""
	}
	q := m.mainBuilt().q
	var parts []string
	if q.byChart {
		charts := 0
		for _, s := range q.sections {
			if s.chart {
				charts++
			}
		}
		parts = append(parts, "by chart")
		if q.issues > 0 {
			parts = append(parts, strconv.Itoa(q.issues)+" "+plural(q.issues, "issue", "issues"))
		}
		if charts > 0 {
			parts = append(parts, strconv.Itoa(charts)+" "+plural(charts, "chart", "charts"))
		}
		if n := q.rowsIn(); n > 0 {
			parts = append(parts, strconv.Itoa(n)+" in no chart")
		}
		return strings.Join(parts, " · ")
	}
	if q.issues > 0 {
		parts = append(parts, strconv.Itoa(q.issues)+" "+plural(q.issues, "issue", "issues"))
	}
	for g := mainGroup(0); g < groupCount; g++ {
		n := len(q.byGroup[g])
		if n == 0 {
			continue
		}
		word := countWords[g]
		if g == groupCharts {
			word = plural(n, "chart", "charts")
		}
		parts = append(parts, strconv.Itoa(n)+" "+word)
	}
	return strings.Join(parts, " · ")
}
