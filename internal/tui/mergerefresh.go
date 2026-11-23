package tui

import tea "github.com/charmbracelet/bubbletea"

// The roster reads a pull request in the last state this machine kept of it and
// never asks the host. What merged on the host is told to the project's list by
// one background read that does ask (`jig next --refresh-merges`, which refreshes
// the kept states older than a listing's age and prints a count), and then the roster
// that reads them. It runs in the background class, so no key waits on it.

// mergeRefreshedMsg says the read that asks the host for the kept states ended.
type mergeRefreshedMsg struct{}

// mainWaitsOnMerge is whether a row of the project's list waits on a merge: a
// ticket that published and has not landed.
func (m *Model) mainWaitsOnMerge() bool {
	for _, id := range m.w.order {
		if end, ok := m.endOf(m.w.tickets[id]); ok && (end.State == StateWaitsToLand || end.State == StateOnLand) {
			return true
		}
	}
	return false
}

// refreshMergesThenRoster reads the kept pull request states from the host in
// the background, then the roster. One refresh is out at a time; while it is,
// the roster is not read ahead of it.
func (m *Model) refreshMergesThenRoster() tea.Cmd {
	if m.run == nil || m.mergeRefreshing {
		return nil
	}
	m.mergeRefreshing = true
	args, run := m.verbArgs("next", "--refresh-merges"), m.run
	return func() tea.Msg {
		run(classBackground, about{}, args)
		return mergeRefreshedMsg{}
	}
}
