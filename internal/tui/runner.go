package tui

import (
	"slices"
	"strings"
	"sync"

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

// serial is r made safe for concurrent use: one call at a time, so a status
// read for a screen never overlaps a publish that a key started.
func serial(r Runner) Runner {
	var mu sync.Mutex
	return func(args []string) (string, int) {
		mu.Lock()
		defer mu.Unlock()
		return r(args)
	}
}

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
}

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
		rows []rosterRow
		err  error
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
			out, code := run(args)
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
		out, _ := run(args)
		return statusMsg{ticket: id, info: parseStatus(out)}
	}
}

// loadRoster reads the store's ticket list, one read at a time.
func (m *Model) loadRoster() tea.Cmd {
	if m.rosterBusy || m.run == nil {
		return nil
	}
	m.rosterBusy = true
	args := m.verbArgs("next", "--json")
	run := m.run
	return func() tea.Msg {
		out, _ := run(args)
		rows, err := parseRoster(out)
		return rosterMsg{rows: rows, err: err}
	}
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
