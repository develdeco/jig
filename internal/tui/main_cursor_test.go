package tui

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// bareMain is a model with no runner and no roster read, whose stream showed
// JIG-1.
func bareMain(withRunner bool) *Model {
	cfg := Config{Zone: time.UTC, Width: 80, Height: 24, Project: "demo", Now: func() time.Time { return rigNow }}
	if withRunner {
		cfg.Run = func([]string) (string, int) { return "", 0 }
	}
	m := New(cfg)
	m.w.apply(jig1())
	return m
}

// TestMainReadsTheStoreBeforeItDraws: with a runner attached, main says it is
// reading the store whatever tickets the stream has shown, and draws the
// roster's rows once it is read; a model with no runner draws what it knows.
func TestMainReadsTheStoreBeforeItDraws(t *testing.T) {
	t.Parallel()
	m := bareMain(true)
	if len(m.w.order) == 0 {
		t.Fatal("the stream named no ticket")
	}
	before := pagesText(m.mainPage())
	if !strings.Contains(before, "reading the store…") || strings.Contains(before, "JIG-1") || strings.Contains(before, "Done") {
		t.Errorf("main before the roster draws the stream's tickets:\n%s", before)
	}
	m.w.setRoster([]rosterRow{{ID: "JIG-1", Title: "Greeting copy", State: "ready"}})
	after := pagesText(m.mainPage())
	if strings.Contains(after, "reading the store…") || !strings.Contains(after, "JIG-1") {
		t.Errorf("main after the roster:\n%s", after)
	}

	// With no runner nothing is read: the stream's ticket is all there is, and
	// it is a ticket nothing is known of: not started.
	if got := pagesText(bareMain(false).mainPage()); strings.Contains(got, "reading the store…") || !strings.Contains(got, "Not started") {
		t.Errorf("a model with no runner does not draw what it knows:\n%s", got)
	}
}

// TestMainCursorFollowsItsRow: a roster that re-sorts leaves the cursor on its
// ticket, a row that goes to another group leaves it there, and a row that
// left the page leaves it on the row at the same place.
func TestMainCursorFollowsItsRow(t *testing.T) {
	t.Parallel()
	m := bareMain(true)
	m.dropTicket("JIG-1")
	// Ready tickets stand in the order `jig next` lists them.
	row := func(order int, id string) rosterRow {
		return rosterRow{ID: id, Title: id, State: "ready", StartOrder: order}
	}
	m.w.setRoster([]rosterRow{row(1, "A-1"), row(2, "A-2"), row(3, "A-3"), row(4, "A-4")})
	m.View()
	m.move(2)
	if ref := m.selected(); ref == nil || ref.id != "A-3" {
		t.Fatalf("the cursor is on %+v, want A-3", ref)
	}

	// The store moves: A-3 is now first to start.
	m.w.setRoster([]rosterRow{row(2, "A-1"), row(3, "A-2"), row(1, "A-3"), row(4, "A-4")})
	m.View()
	if ref := m.selected(); ref == nil || ref.id != "A-3" || m.top().sel != 0 {
		t.Errorf("after a re-sort the cursor is on %+v at %d, want A-3 at 0", ref, m.top().sel)
	}

	// The row goes to another group: the cursor goes with it.
	m.w.setRoster([]rosterRow{row(1, "A-1"), row(2, "A-2"), {ID: "A-3", Title: "A-3", State: "blocked", Blockers: []chartWait{{Ticket: "A-1", Kind: "merged"}}}, row(3, "A-4")})
	m.View()
	if ref := m.selected(); ref == nil || ref.id != "A-3" || m.top().sel != 3 {
		t.Errorf("after the row went to waiting the cursor is on %+v at %d, want A-3 at 3", ref, m.top().sel)
	}

	// The row leaves: the cursor stays at its place, on the row there.
	m.dropTicket("A-3")
	m.View()
	if ref := m.selected(); ref == nil || ref.id != "A-4" {
		t.Errorf("after the row left the cursor is on %+v at %d, want the nearest row, A-4", ref, m.top().sel)
	}
	m.dropTicket("A-4")
	m.View()
	if ref := m.selected(); ref == nil || ref.id != "A-2" {
		t.Errorf("after the last row left the cursor is on %+v, want the nearest, A-2", ref)
	}
}

// dropTicket takes a ticket out of what the screens know, as a store that no
// longer holds it would.
func (m *Model) dropTicket(id string) {
	m.w.gen++
	delete(m.w.tickets, id)
	m.w.order = slices.DeleteFunc(m.w.order, func(x string) bool { return x == id })
}

// TestMainCursorOnAFoldedRowStaysOnItsFold: a row the roster moved into a
// closed fold leaves the cursor on the fold row, and opening the fold shows
// the row.
func TestMainCursorOnAFoldedRowStaysOnItsFold(t *testing.T) {
	t.Parallel()
	m := bareMain(true)
	m.w.setRoster([]rosterRow{{ID: "A-1", Title: "A-1", State: "ready", StartOrder: 1}, {ID: "A-2", Title: "A-2", State: "ready", StartOrder: 2}})
	m.View()
	m.move(1)
	if ref := m.selected(); ref == nil || ref.id != "A-2" {
		t.Fatalf("the cursor is on %+v, want A-2", ref)
	}
	m.w.setRoster([]rosterRow{{ID: "A-1", Title: "A-1", State: "ready", StartOrder: 1}, {ID: "A-2", Title: "A-2", State: "new"}})
	m.View()
	if ref := m.selected(); ref == nil || ref.kind != rowFold || ref.id != "not started" {
		t.Fatalf("the cursor is on %+v, want the fold row of not started", ref)
	}
	m.open()
	if text := pagesText(m.mainPage()); !strings.Contains(text, "A-2") {
		t.Errorf("enter on the fold row does not show its rows:\n%s", text)
	}
	m.open()
	if text := pagesText(m.mainPage()); strings.Contains(text, "A-2") {
		t.Errorf("enter on an open fold row does not fold them back:\n%s", text)
	}
}
