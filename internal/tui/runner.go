package tui

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Runner runs one jig command in process, as the person would have typed it
// after `jig`, and returns what it printed and its exit code. The screens act
// only through it: answering, deciding an ask, opening a gate round and
// publishing are verbs, so the TUI can do nothing a command line cannot and
// every action leaves the lines in the journal that the command writes.
//
// A Runner need not be safe for concurrent use: the model calls it through
// serial.
type Runner func(args []string) (output string, code int)

// ranStep is one command that ran.
type ranStep struct {
	Args []string
	Out  string
	Code int
}

// outcome is what a key's command (or its two, for publish) did.
type outcome struct {
	Label  string
	Ticket string
	Steps  []ranStep
	// Print is where the ticket (or the chart on show) stood at the first roster
	// read (the chart's read, for a line with no ticket) that began after the
	// command's result arrived, so the command's own effect (a
	// refusal it journaled, an approval it wrote) is part of it; the line the
	// panel prints for it is gone once a later read differs. Fixed is set once
	// it is taken.
	Print string
	Fixed bool
	// After is how many roster reads had begun when the result arrived: a read
	// numbered above it started after the command and is the first to fix Print.
	After int
}

// leavesTheMove says the verb's result leaves the next move to someone else (the
// gate opens a round for a reviewer), so its help line is shown whole.
func (o outcome) leavesTheMove() bool { return o.Label == "gate" || o.Label == labelPublishWhat }

// labelPublishWhat is the label of the render `p` runs first: what it leaves on
// the panel is a What that waits for its bullets.
const labelPublishWhat = "publish-what"

// shown is the step the screen reports: the one that failed, else the last.
func (o outcome) shown() ranStep {
	if len(o.Steps) == 0 {
		return ranStep{}
	}
	return o.Steps[len(o.Steps)-1]
}

// The messages the commands report back with.
type (
	// ranMsg is a key's command, done.
	ranMsg struct{ result outcome }
	// statusMsg is a ticket's status, read.
	statusMsg struct {
		ticket string
		info   *statusInfo
	}
	// rosterMsg is the store's ticket list, read.
	rosterMsg struct {
		rows   []rosterRow
		charts []chartListing
		err    error
		// seq is the read's place among the roster reads begun.
		seq int
	}
)

// verbArgs is the command verb rest... followed by the flags that keep every
// command on the store the screens were opened on.
func (m *Model) verbArgs(verb string, rest ...string) []string {
	return slices.Concat([]string{verb}, rest, m.cfg.Scope)
}

// exec runs the steps in order, each a command line, and stops at the first
// that fails.
func (m *Model) exec(label, ticket string, steps ...[]string) tea.Cmd {
	run := m.run
	return func() tea.Msg {
		res := outcome{Label: label, Ticket: ticket}
		for _, args := range steps {
			out, code := run(classKey, about{}, args)
			res.Steps = append(res.Steps, ranStep{Args: args, Out: out, Code: code})
			if code != 0 {
				break
			}
		}
		return ranMsg{result: res}
	}
}

// refreshStatus reads the ticket's status, unless one is being read, in which
// case the ticket is marked to be read again when it lands.
func (m *Model) refreshStatus(id string) tea.Cmd {
	t := m.w.tickets[id]
	if t == nil || m.run == nil || t.awaiting {
		return nil
	}
	if t.loading {
		t.dirty = true
		return nil
	}
	t.loading, t.dirty = true, false
	m.lastRead = m.now()
	args := m.verbArgs("status", id)
	run := m.run
	return func() tea.Msg {
		out, _ := run(classOpened, about{ticket: id}, args)
		return statusMsg{ticket: id, info: parseStatus(out)}
	}
}

// loadRoster reads the store's ticket list, one read at a time: the roster, the
// rows of every ticket and the charts, which is all a screen reads of the store.
func (m *Model) loadRoster() tea.Cmd {
	if m.rosterBusy || m.run == nil {
		return nil
	}
	m.rosterBusy, m.rosterStale, m.lastRoster = true, false, m.now()
	m.rosterStarted++
	seq := m.rosterStarted
	args := m.verbArgs("next", "--json", "--roster")
	run := m.run
	return func() tea.Msg {
		out, _ := run(classBackground, about{}, args)
		rows, charts, err := parseRoster(out)
		return rosterMsg{rows: rows, charts: charts, err: err, seq: seq}
	}
}

// loadChart reads a chart's standing, one read of a chart at a time.
func (m *Model) loadChart(name string) tea.Cmd {
	c := m.w.charts[name]
	if c == nil || c.loading || m.run == nil {
		return nil
	}
	c.loading, c.dirty = true, false
	m.lastRead = m.now()
	args := m.verbArgs("next", name, "--json")
	run := m.run
	return func() tea.Msg {
		out, _ := run(classOpened, about{chart: name}, args)
		info, err := parseChart(out)
		return chartMsg{name: name, info: info, err: err}
	}
}

// syncScreen tells the queue which screen is on show, so the reads about it run
// first: the ticket the screen is of, or the chart and the tickets and issue it
// lists.
func (m *Model) syncScreen() {
	if m.queue == nil {
		return
	}
	var on onShow
	switch e := m.top(); e.screen {
	case screenMain:
	case screenChart:
		on.chart = e.chart
		if c := m.w.charts[e.chart]; c != nil {
			on.chartTickets = c.ids()
			if issue := c.issueID(); issue != "" {
				on.chartTickets = append(on.chartTickets, issue)
			}
		}
	default:
		on.ticket = e.ticket
	}
	m.queue.show(on)
}

// shownLine is the command as the person would type it, without the flags the
// screens add to keep it on the store.
func (m *Model) shownLine(args []string) string {
	return "jig " + strings.Join(stripScope(args, m.cfg.Scope), " ")
}

// stripScope removes the scope flags (a run of arguments) from args.
func stripScope(args, scope []string) []string {
	if len(scope) == 0 {
		return args
	}
	for i := 0; i+len(scope) <= len(args); i++ {
		if slices.Equal(args[i:i+len(scope)], scope) {
			return slices.Concat(args[:i], args[i+len(scope):])
		}
	}
	return args
}
