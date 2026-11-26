package tui

import (
	"slices"

	tea "github.com/charmbracelet/bubbletea"
)

// The moves between screens. The screens form a stack: a move that goes into a
// screen pushes it, a move that goes to another screen of the same ticket
// replaces what is above the ticket's own, so `esc` always goes up one level.
// A screen that is left keeps where the cursor stood for the session, and is
// opened there by any move that comes back to it.

// navKey names a screen a person can leave and come back to.
type navKey struct {
	screen               screenKind
	ticket, slice, chart string
}

func keyOf(e navEntry) navKey {
	return navKey{screen: e.screen, ticket: e.ticket, slice: e.slice, chart: e.chart}
}

// cut leaves every screen above the first n, keeping where the cursor stood on
// each. The project's list is never left.
func (m *Model) cut(n int) {
	n = max(n, 1)
	for i := len(m.nav) - 1; i >= n; i-- {
		if m.kept == nil {
			m.kept = map[navKey]navEntry{}
		}
		m.kept[keyOf(m.nav[i])] = m.nav[i]
	}
	if n < len(m.nav) {
		m.nav = m.nav[:n]
	}
}

// arrive gives a screen that is being opened the cursor, the scroll and the
// follow flag it was left with, and says whether it had any.
func (m *Model) arrive(e *navEntry) bool {
	k, ok := m.kept[keyOf(*e)]
	if ok {
		e.sel, e.scroll, e.follow = k.sel, k.scroll, k.follow
		e.paneFocus, e.paneCurs, e.textFocus, e.zoom = k.paneFocus, k.paneCurs, k.textFocus, k.zoom
		e.lead, e.psearch = k.lead, k.psearch
		e.seenSel, e.seenSet = k.seenSel, k.seenSet
	}
	return ok
}

// clampCursor keeps the cursor of the screen on top inside the rows it has now.
func (m *Model) clampCursor() {
	if n := len(m.page().refs()); n > 0 {
		e := m.top()
		e.sel = min(e.sel, n-1)
	}
}

// goTo keeps the first keep screens, puts entries above them, and reads what the
// screen it ends on needs: ref is the row a move came from, nil for a key.
func (m *Model) goTo(keep int, ref *rowRef, entries ...navEntry) tea.Cmd {
	m.cut(keep)
	restored := false
	for i := range entries {
		restored = m.arrive(&entries[i])
		m.nav = append(m.nav, entries[i])
	}
	m.last = nil
	e := m.top()
	if restored {
		m.clampCursor()
	}
	if e.screen == screenReview {
		// What the review screen is for, when something waits, is deciding it:
		// the cursor is on the ask it was opened from, else the first one, unless
		// the person left it somewhere.
		if !restored || (ref != nil && ref.kind == rowAsk) {
			e.sel = m.askRow(ref)
		}
		if !restored {
			// The page opens at its top, so the rounds are read before the rows.
			e.lead = 1 << 20
		}
	}
	var cmds []tea.Cmd
	if e.screen == screenChart {
		cmds = append(cmds, m.enterChart(e)...)
	}
	if t := m.w.tickets[e.ticket]; t != nil && (t.status == nil || t.dirty) {
		cmds = append(cmds, m.refreshStatus(e.ticket))
	}
	if e.screen == screenBrief {
		cmds = append(cmds, m.loadBrief(e.ticket))
	}
	return tea.Batch(cmds...)
}

// stepScreens are the screens the keys 1 to 5 open: a ticket's five steps.
var stepScreens = [...]screenKind{screenBrief, screenBuild, screenReview, screenPublish, screenLand}

// stepsReason is why the step keys cannot run on the screen on top, "" when
// they can.
func (m *Model) stepsReason() string {
	id := m.top().ticket
	switch t := m.w.tickets[id]; {
	case t == nil || t.awaiting:
		return id + " does not exist yet"
	case t.digest == nil:
		return readingText
	case isMulti(t):
		return id + " is a multi-task: it has tasks, not steps"
	}
	return ""
}

// openStep is a step key: the ticket's step n opens as a child of the ticket's
// own screen, from any screen of the ticket, with the screens above it
// replaced. The screen already showing is left as it is.
func (m *Model) openStep(n int) tea.Cmd {
	e := m.top()
	step := stepScreens[n-1]
	if e.screen == step {
		return nil
	}
	id := e.ticket
	// The ticket's own screen is the lowest of the run of its screens on top.
	i := len(m.nav) - 1
	for i > 0 && m.nav[i-1].ticket == id {
		i--
	}
	entries := []navEntry{{screen: step, ticket: id}}
	keep := i + 1
	if m.nav[i].screen != screenTicket {
		// Reached some other way: the ticket's screen is put under the step.
		keep = i
		entries = append([]navEntry{{screen: screenTicket, ticket: id, follow: true}}, entries...)
	}
	return m.goTo(keep, nil, entries...)
}

// openMain is `h`: the stack is cut to the project's list.
func (m *Model) openMain() tea.Cmd {
	m.cut(1)
	m.last = nil
	return nil
}

// chartsOf are the charts that list the ticket id, in the order the store lists
// them.
func (m *Model) chartsOf(id string) []string {
	var out []string
	for _, name := range m.w.chartOrder {
		if c := m.w.charts[name]; c != nil && slices.Contains(c.listing.Tickets, id) {
			out = append(out, name)
		}
	}
	return out
}
