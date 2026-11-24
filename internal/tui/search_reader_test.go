package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestEscAfterSwitchingViewPutsTheCursorBackOnItsRow: the search began with the
// cursor on a row of the view by state; the view was switched to by chart, where
// the row is drawn under its chart, and esc puts the cursor on that row, not on
// the row at the same place.
func TestEscAfterSwitchingViewPutsTheCursorBackOnItsRow(t *testing.T) {
	t.Parallel()
	m := searchStore(t)
	m.View()
	for underCursor(m) != "WAI-3" {
		press(m, tea.KeyDown)
	}
	typedDrawn(m, "/wai")
	press(m, tea.KeyEnter)
	press(m, tea.KeyEsc)
	if m.top().screen != screenMain {
		t.Fatalf("esc did not come back to main")
	}
	typed(m, "s")
	if got := drawn(m, tea.KeyMsg{Type: tea.KeyEsc}); got != "WAI-3" || underCursor(m) != "WAI-3" {
		t.Errorf("esc in the other view marks %q and keys act on %q, want WAI-3", got, underCursor(m))
	}
}

// TestEscOnMainWithNoSearchIsNoKey: with no search on, esc on main changes no
// frame and says nothing, also when it is the second esc after coming back from
// a ticket.
func TestEscOnMainWithNoSearchIsNoKey(t *testing.T) {
	t.Parallel()
	m := searchStore(t)
	before := stripANSI(m.View())
	press(m, tea.KeyEsc)
	if m.notice != "" || stripANSI(m.View()) != before {
		t.Errorf("esc on main with no search said %q or changed the frame", m.notice)
	}
	typed(m, "/sort")
	press(m, tea.KeyEnter)
	press(m, tea.KeyEsc)
	press(m, tea.KeyEsc)
	cleared := stripANSI(m.View())
	press(m, tea.KeyEsc)
	if m.notice != "" || stripANSI(m.View()) != cleared {
		t.Errorf("the esc after the search was cleared said %q or changed the frame", m.notice)
	}
}

// TestEscAfterSwitchingViewFindsTheFoldThatHidesItsRow: the search began on a
// closed ticket a chart lists, in the view by chart; the view was switched to
// by state, which folds it away, and esc puts the cursor on the fold row that
// hides it.
func TestEscAfterSwitchingViewFindsTheFoldThatHidesItsRow(t *testing.T) {
	t.Parallel()
	m := searchStore(t)
	m.w.setCharts(append(chartListings(m), chartListing{Name: "closed-run", Title: "A run", Tickets: []string{"CLS-4"}}))
	typed(m, "s")
	m.View()
	press(m, tea.KeyHome)
	for i := 0; underCursor(m) != "CLS-4"; i++ {
		if i > 20 {
			t.Fatalf("the cursor never reached CLS-4 among %v, on %q", shown(m), underCursor(m))
		}
		press(m, tea.KeyDown)
	}
	typedDrawn(m, "/cls")
	press(m, tea.KeyEnter)
	press(m, tea.KeyEsc)
	typed(m, "s")
	if got := drawn(m, tea.KeyMsg{Type: tea.KeyEsc}); got != "done" || underCursor(m) != "done" {
		t.Errorf("esc marks %q and keys act on %q, want the done fold row", got, underCursor(m))
	}
}

// TestLitLettersAreOnlyTheRowsOwnWords: a word a cell says that is not one of
// the row's words lights nothing, even when its letters start the query; the
// row is listed for the words it has.
func TestLitLettersAreOnlyTheRowsOwnWords(t *testing.T) {
	t.Parallel()
	m := searchStoreIn(t, styledConfig())
	m.w.setRoster(append(rosterRows(m), rosterRow{ID: "MRG-5", Title: "Merged on the host",
		Digest: &digestInfo{Kind: "issue", State: "published", PRs: []digestPR{{Repo: "demo", Number: 9, State: "merged", Target: "main"}}}}))
	m.w.setCharts(append(chartListings(m), chartListing{Name: "notebook-run", Title: "A run", Tickets: []string{"MRG-5"}}))
	typed(m, "/not")
	line := styledLine(t, m, "MRG-5")
	if !strings.Contains(stripANSI(line), "not recorded") {
		t.Fatalf("the row does not say the merge was not recorded: %q", stripANSI(line))
	}
	if got := litSpans(line); len(got) != 0 {
		t.Errorf("/not lit %q in a row kept by its chart", got)
	}
	// The row's own words are lit where it draws them.
	press(m, tea.KeyEsc)
	typed(m, "/merg")
	if got := strings.Join(litSpans(styledLine(t, m, "MRG-5")), "|"); got != "Merg|merg" {
		t.Errorf("/merg lit %q, want the title and the detail words the row holds", got)
	}
}

// TestSearchBuildsNoRowDetail: opening the search and typing builds no row's
// detail, and after a store move the rebuilt page's details are main's own, not
// built a second time for the search.
func TestSearchBuildsNoRowDetail(t *testing.T) {
	t.Parallel()
	m := searchStore(t)
	m.nav[0].open = map[string]bool{"done": true, "not started": true}
	m.View()
	one := m.detailBuilds
	if one == 0 {
		t.Fatalf("main built no detail")
	}
	for _, r := range "/sort" {
		typed(m, string(r))
		m.View()
	}
	if m.detailBuilds != one {
		t.Errorf("the search built %d details", m.detailBuilds-one)
	}
	m.w.gen++
	m.View()
	typed(m, "o")
	m.View()
	if got := m.detailBuilds - one; got != one {
		t.Errorf("a store move and a key built %d details, want main's %d alone", got, one)
	}
}
