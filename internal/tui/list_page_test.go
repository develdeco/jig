package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TestATicketFrameBuildsNoListPage: with 250 tickets on the project's list, a
// frame of a ticket's screen builds the page of that screen and no list page for
// its sibling place, and a down key builds no page beyond the screen's own; the
// counter counts every build a list read makes.
func TestATicketFrameBuildsNoListPage(t *testing.T) {
	t.Parallel()
	m := bigStore(t, 250, 0, 250)
	_ = m.View() // main, built once
	m.nav = append(m.nav, navEntry{screen: screenTicket, ticket: "T-0100", follow: true})
	m.View()

	before := m.pageBuilds
	started := time.Now()
	for range 20 {
		m.View()
	}
	frames := time.Since(started) / 20
	if got := m.pageBuilds - before; got != 20 {
		t.Errorf("20 frames of a ticket built %d pages, want 20 (its own, one each)", got)
	}
	if s := m.sibs(); s == nil || s.at != 100 {
		t.Fatalf("the ticket's place among the list is %+v, want 100", s)
	}

	before = m.pageBuilds
	started = time.Now()
	for range 20 {
		_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m.View()
	}
	keys := time.Since(started) / 20
	if got := m.pageBuilds - before; got > 3*20 {
		t.Errorf("20 down keys with their frames built %d pages, want at most 3 each", got)
	}
	t.Logf("250 tickets: 200 ticket frames %v, 50 down keys %v", frames*200, keys*50)

	// A list that is not the project's is built, and counted.
	m.nav = []navEntry{m.nav[0], {screen: screenChart, chart: "x"}, {screen: screenTicket, ticket: "T-0001"}}
	before = m.pageBuilds
	m.refsOf(1)
	if m.pageBuilds == before {
		t.Errorf("a list page was built and not counted")
	}
}
