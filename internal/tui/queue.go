package tui

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/events"
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
}

// ref is what the cursor stands on when it is on the row.
func (r queueRow) ref() rowRef {
	if r.chart {
		return rowRef{kind: rowChart, id: r.id}
	}
	return rowRef{kind: rowTicket, id: r.id, ticket: r.id}
}

// queue is the project's list, grouped.
type queue struct {
	rows    []queueRow
	byGroup [groupCount][]queueRow
	// idW and titleW are the columns of the ticket rows; chartIDW and
	// chartTitleW the chart rows'. They hold for every row, folded or not, so a
	// fold opened moves nothing.
	idW, titleW, chartIDW, chartTitleW int
}

// queue reads the roster's rows into the groups. A ticket the roster did not
// list (an older one the stream showed) stands in done.
func (m *Model) queue() queue {
	var q queue
	q.idW = 4
	for _, id := range m.w.order {
		r := m.ticketRow(id)
		q.rows = append(q.rows, r)
		q.idW = max(q.idW, width(id))
		q.titleW = max(q.titleW, width(r.t.title))
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
	for _, r := range q.rows {
		q.byGroup[r.group] = append(q.byGroup[r.group], r)
	}
	for g := range q.byGroup {
		sortGroup(mainGroup(g), q.byGroup[g])
	}
	return q
}

// ticketRow is the row of ticket id and the group the first rule that holds
// puts it in.
func (m *Model) ticketRow(id string) queueRow {
	r := m.rowOf(id, "", nil)
	t := r.t
	if t.digest == nil {
		// A ticket the roster gave no digest to stands as its status says.
		r.steps = m.steps(t)
	}
	row := queueRow{id: id, t: t, steps: r.steps, cur: r.steps[currentStep(r.steps)], at: t.lastActivity(), next: t.readyAt}
	d := t.digest
	unmet := len(t.blockers) > 0 || r.behind || t.state == "blocked" || (d != nil && len(d.LandFirst) > 0)
	switch {
	case r.needs:
		row.group = groupNeeds
	case m.ticketLive(t) || (t.rostered && t.state != "new" && !r.atEnd && !row.cur.Done && row.cur.State != StateQueued && row.cur.State != StateReady):
		// A step that is not done and is not waiting to start is under way, a
		// stalled or failed one too: its builder has not finished with it. A
		// ticket the roster does not list is under way only while a session is,
		// and one it says is new has no work begun, its brief being drafted
		// or not.
		row.group = groupRunning
	case !unmet && t.state != "new" && ((d != nil && d.Ready) || (d == nil && t.state == "ready")):
		// A new ticket's digest says its brief can start; it is not started.
		row.group = groupReady
	case unmet:
		row.group = groupWaiting
	case r.atEnd || !t.rostered:
		row.group = groupDone
	default:
		row.group = groupNotStarted
	}
	return row
}

// chartRow is the row of a chart that plans no issue. It is in Needs you when
// one of its tickets waits for the person or its landing does, Running when a
// session is at work on it, Done when its run is, and the Charts group
// otherwise.
func (m *Model) chartRow(name string, c *chart) queueRow {
	row := queueRow{id: name, chart: true, group: groupCharts}
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
		return naturalLess(a.id, b.id)
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
	if !m.w.rosterRead && m.run != nil {
		return ""
	}
	q := m.queue()
	var parts []string
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
