package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// underCursor is the id of the row under the cursor, "" for none.
func underCursor(m *Model) string {
	if ref := m.selected(); ref != nil {
		return ref.id
	}
	return ""
}

func rosterRows(m *Model) []rosterRow {
	var rows []rosterRow
	for _, id := range m.w.order {
		t := m.w.tickets[id]
		rows = append(rows, rosterRow{ID: id, Title: t.title, Digest: t.digest})
	}
	return rows
}

func chartListings(m *Model) []chartListing {
	var out []chartListing
	for _, name := range m.w.chartOrder {
		out = append(out, m.w.charts[name].listing)
	}
	return out
}

// TestSearchPutsTheCursorOnTheFirstMatch: each key that changes the query puts
// the cursor on the first row that matches by itself, an issue kept only for
// its task's sake not counting, and the moves go over the rows drawn.
func TestSearchPutsTheCursorOnTheFirstMatch(t *testing.T) {
	t.Parallel()
	m := searchStore(t)
	press(m, tea.KeyEnd)
	if got := underCursor(m); got != "done" {
		t.Fatalf("the cursor is on %q after end, want the last row, the done fold", got)
	}
	typed(m, "/sort")
	if got := underCursor(m); got != "TSK-1" {
		t.Errorf("/sort puts the cursor on %q, want TSK-1, not the issue kept for it", got)
	}
	typed(m, " x")
	press(m, tea.KeyBackspace)
	press(m, tea.KeyBackspace)
	if got := underCursor(m); got != "TSK-1" {
		t.Errorf("backspace puts the cursor on %q, want TSK-1", got)
	}
	// Every row has a word that starts with t: the cursor is on the first drawn.
	press(m, tea.KeyEsc)
	press(m, tea.KeyEnd)
	typed(m, "/t")
	rows := shown(m)
	if got := underCursor(m); got != rows[0] {
		t.Errorf("/t puts the cursor on %q, want the first row drawn, %q", got, rows[0])
	}
	press(m, tea.KeyDown)
	if got := underCursor(m); got != rows[1] {
		t.Errorf("down moves to %q, want %q", got, rows[1])
	}
	press(m, tea.KeyEnd)
	if got := underCursor(m); got != rows[len(rows)-1] {
		t.Errorf("end moves to %q, want the last row drawn %q", got, rows[len(rows)-1])
	}
	press(m, tea.KeyHome)
	if got := underCursor(m); got != rows[0] {
		t.Errorf("home moves to %q, want %q", got, rows[0])
	}
	press(m, tea.KeyPgDown)
	if want := rows[min(5, len(rows)-1)]; underCursor(m) != want {
		t.Errorf("page down moves to %q, want %q", underCursor(m), want)
	}
	press(m, tea.KeyPgUp)
	if underCursor(m) != rows[0] {
		t.Errorf("page up moves to %q, want %q", underCursor(m), rows[0])
	}
	// No row matches: there is no row under the cursor, and enter opens none.
	typed(m, "zzz")
	if got := underCursor(m); got != "" {
		t.Errorf("a query nothing matches has the cursor on %q", got)
	}
	press(m, tea.KeyEnter)
	if m.top().screen != screenMain || !m.nav[0].search.open {
		t.Errorf("enter on no row left the search line")
	}
}

// TestSearchEscPutsTheCursorBack: esc clears the search and the cursor is on the
// row it stood on when `/` opened the line, however far down, and wherever the
// store has moved it since, or on the nearest row when it left.
func TestSearchEscPutsTheCursorBack(t *testing.T) {
	t.Parallel()
	m := searchBig(t, 1000)
	m.View()
	for range 400 {
		press(m, tea.KeyDown)
	}
	from := underCursor(m)
	if from == "" || from == "T-0000" {
		t.Fatalf("the cursor did not move: %q", from)
	}
	typed(m, "/t-00")
	typed(m, "5")
	if got := underCursor(m); got == from {
		t.Fatalf("the cursor stayed on %q while searching", got)
	}
	press(m, tea.KeyEsc)
	if got := underCursor(m); got != from {
		t.Errorf("esc puts the cursor on %q, want %q", got, from)
	}
	if frame := stripANSI(m.View()); !strings.Contains(frame, from) {
		t.Errorf("the row %q the cursor is back on is not in the window:\n%s", from, frame)
	}

	// A row the store moved while the search was on: the cursor finds it where
	// main now draws it.
	s := searchStore(t)
	press(s, tea.KeyDown)
	press(s, tea.KeyDown)
	press(s, tea.KeyDown)
	from = underCursor(s)
	typed(s, "/drop")
	rows := rosterRows(s)
	moved := append([]rosterRow{{ID: "AAA-0", Title: "New ticket", Digest: rows[1].Digest}}, rows...)
	_, _ = s.Update(rosterMsg{rows: moved, charts: chartListings(s), seq: 9})
	press(s, tea.KeyEsc)
	if got := underCursor(s); got != from {
		t.Errorf("esc after a store move puts the cursor on %q, want %q", got, from)
	}

	// A row that left the store: the cursor is on a row, not nowhere.
	typed(s, "/drop")
	var without []rosterRow
	for _, r := range rosterRows(s) {
		if r.ID != from {
			without = append(without, r)
		}
	}
	_, _ = s.Update(rosterMsg{rows: without, charts: chartListings(s), seq: 10})
	press(s, tea.KeyEsc)
	if got := underCursor(s); got == "" || got == from {
		t.Errorf("esc after the row left puts the cursor on %q, want the nearest row", got)
	}
}

// TestSearchLettersTypedTogetherAreText: letters that arrive in one key (a paste,
// or fast typing) are text, whatever name of a key they spell.
func TestSearchLettersTypedTogetherAreText(t *testing.T) {
	t.Parallel()
	m := searchStore(t)
	typed(m, "/")
	for _, word := range []string{"up", "down", "esc", "enter", "end"} {
		_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(word)})
	}
	if got := string(m.nav[0].search.query); got != "updownescenterend" || m.top().screen != screenMain {
		t.Errorf("the line holds %q on %v, want the letters as text", got, m.top().screen)
	}
}

// TestSearchStaysOnWhenMainIsComeBackTo: opening a row leaves the search applied
// with the line closed, so every key acts as on main, and esc from the ticket
// comes back to the narrowed list; `s` keeps the query; esc clears it.
func TestSearchStaysOnWhenMainIsComeBackTo(t *testing.T) {
	t.Parallel()
	m := searchStore(t)
	press(m, tea.KeyDown)
	from := underCursor(m)
	typed(m, "/sort")
	narrowed := stripANSI(m.View())
	press(m, tea.KeyEnter)
	if e := m.top(); e.screen != screenTicket || e.ticket != "TSK-1" {
		t.Fatalf("enter opened %+v, want the ticket TSK-1", e)
	}
	press(m, tea.KeyEsc)
	if m.top().screen != screenMain {
		t.Fatalf("esc did not come back to main")
	}
	if s := m.nav[0].search; s.open || string(s.query) != "sort" {
		t.Errorf("the search is %+v coming back, want the query kept and the line closed", s)
	}
	if got := stripANSI(m.View()); !strings.Contains(got, "search “sort” · 1 of 7") || strings.Contains(got, "PUB-11") || underCursor(m) != "TSK-1" {
		t.Errorf("main coming back is not the narrowed list it was:\n%s\n--- before\n%s", got, narrowed)
	}
	if hint := stripANSI(strings.Join(m.footer(110), "\n")); !strings.Contains(hint, "esc clear search") || !strings.Contains(hint, "/ search") {
		t.Errorf("the hint line of a search applied does not list esc and /:\n%s", hint)
	}
	// `s` is the key it is on main, and the query is kept.
	typed(m, "s")
	if !m.nav[0].byChart || string(m.nav[0].search.query) != "sort" || m.nav[0].search.open {
		t.Errorf("s did not switch the view with the search kept: %+v", m.nav[0])
	}
	if got := stripANSI(m.View()); strings.Contains(got, "PUB-11") || !strings.Contains(got, "TSK-1") {
		t.Errorf("the view by chart is not narrowed:\n%s", got)
	}
	typed(m, "s")
	// `/` opens the line again on the same query.
	typed(m, "/")
	if !m.nav[0].search.open || string(m.nav[0].search.query) != "sort" {
		t.Errorf("/ did not open the line on the query: %+v", m.nav[0].search)
	}
	press(m, tea.KeyEnter)
	press(m, tea.KeyEsc)
	// esc clears it, and the cursor is where the search began.
	press(m, tea.KeyEsc)
	if m.nav[0].search.applied() || underCursor(m) != from {
		t.Errorf("esc left search %+v with the cursor on %q, want it cleared and on %q", m.nav[0].search, underCursor(m), from)
	}
	// q quits with the search applied and the line closed.
	typed(m, "/sort")
	press(m, tea.KeyEnter)
	press(m, tea.KeyEsc)
	typed(m, "q")
	if !m.quitting {
		t.Errorf("q did not quit main with a search applied")
	}
}

// TestSearchSurvivesAStoreMove: a roster read during a search keeps the query,
// the cursor's row where it still matches, and applies the search to the new
// rows.
func TestSearchSurvivesAStoreMove(t *testing.T) {
	t.Parallel()
	m := searchStore(t)
	typed(m, "/notes")
	press(m, tea.KeyDown)
	if got := underCursor(m); got != "WAI-3" {
		t.Fatalf("the cursor is on %q, want WAI-3", got)
	}
	rows := rosterRows(m)
	more := append([]rosterRow{{ID: "NEW-9", Title: "Release notes again", Digest: rows[3].Digest}}, rows...)
	_, _ = m.Update(rosterMsg{rows: more, charts: chartListings(m), seq: 5})
	if got := string(m.nav[0].search.query); got != "notes" {
		t.Errorf("the query is %q after the store moved", got)
	}
	if ids := strings.Join(shown(m), " "); !strings.Contains(ids, "NEW-9") || !strings.Contains(ids, "WAI-3") || strings.Contains(ids, "TSK-1") {
		t.Errorf("the search is not applied to the new rows: %s", ids)
	}
	if got := underCursor(m); got != "WAI-3" {
		t.Errorf("the cursor moved to %q, want it on WAI-3 where it still matches", got)
	}
	// Its row stops matching: the cursor is on a row that is drawn.
	for i := range more {
		if more[i].ID == "WAI-3" {
			more[i].Title = "Wait for the news"
		}
	}
	_, _ = m.Update(rosterMsg{rows: more, charts: chartListings(m), seq: 6})
	if got := underCursor(m); got == "" {
		t.Errorf("the cursor is nowhere after its row stopped matching")
	}
	if ids := strings.Join(shown(m), " "); strings.Contains(ids, "WAI-3") {
		t.Errorf("a row that no longer matches is drawn: %s", ids)
	}
}
