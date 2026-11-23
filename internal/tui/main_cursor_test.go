package tui

import (
	"strings"
	"testing"
	"time"
)

// TestMainReadsTheStoreBeforeItDraws: with a runner attached, main says it is
// reading the store whatever tickets the stream has shown, and draws the
// roster's rows once it is read; a model with no runner draws what it knows.
func TestMainReadsTheStoreBeforeItDraws(t *testing.T) {
	t.Parallel()
	cfg := Config{Run: func([]string) (string, int) { return "", 0 }, Zone: time.UTC, Width: 80, Height: 24, Project: "demo",
		Now: func() time.Time { return rigNow }}
	m := New(cfg)
	m.w.apply(jig1())
	if len(m.w.order) == 0 {
		t.Fatal("the stream named no ticket")
	}
	before := pagesText(m.mainPage())
	if !strings.Contains(before, "reading the store…") || strings.Contains(before, "JIG-1") {
		t.Errorf("main before the roster draws the stream's tickets:\n%s", before)
	}
	m.w.setRoster([]rosterRow{{ID: "JIG-1", Title: "Greeting copy", State: "ready"}})
	after := pagesText(m.mainPage())
	if strings.Contains(after, "reading the store…") || !strings.Contains(after, "JIG-1") {
		t.Errorf("main after the roster:\n%s", after)
	}

	cfg.Run = nil
	bare := New(cfg)
	bare.w.apply(jig1())
	if got := pagesText(bare.mainPage()); strings.Contains(got, "reading the store…") || !strings.Contains(got, "JIG-1") {
		t.Errorf("a model with no runner does not draw what it knows:\n%s", got)
	}
}

// TestMainCursorFollowsItsRow: a roster that re-sorts leaves the cursor on its
// ticket, and a row that left the page leaves it on the row at the same place.
func TestMainCursorFollowsItsRow(t *testing.T) {
	t.Parallel()
	cfg := Config{Run: func([]string) (string, int) { return "", 0 }, Zone: time.UTC, Width: 80, Height: 24, Project: "demo",
		Now: func() time.Time { return rigNow }}
	m := New(cfg)
	row := func(id string) rosterRow { return rosterRow{ID: id, Title: id, State: "ready"} }
	m.w.setRoster([]rosterRow{row("A-1"), row("A-2"), row("A-3"), row("A-4")})
	m.View()
	m.move(2)
	if ref := m.selected(); ref == nil || ref.id != "A-3" {
		t.Fatalf("the cursor is on %+v, want A-3", ref)
	}

	// The store moves: A-3 is now first.
	m.w.setRoster([]rosterRow{row("A-3"), row("A-1"), row("A-2"), row("A-4")})
	m.View()
	if ref := m.selected(); ref == nil || ref.id != "A-3" || m.top().sel != 0 {
		t.Errorf("after a re-sort the cursor is on %+v at %d, want A-3 at 0", ref, m.top().sel)
	}

	// The row leaves: the cursor stays at its place, on the row there.
	m.w.order = []string{"A-1", "A-2", "A-4"}
	delete(m.w.tickets, "A-3")
	m.View()
	if ref := m.selected(); ref == nil || ref.id != "A-1" {
		t.Errorf("after the row left the cursor is on %+v, want A-1", ref)
	}

	// The last row leaves: the nearest one is the last.
	m.move(1 << 20)
	if ref := m.selected(); ref == nil || ref.id != "A-4" {
		t.Fatalf("end puts the cursor on %+v, want A-4", ref)
	}
	m.w.order = []string{"A-1", "A-2"}
	delete(m.w.tickets, "A-4")
	m.View()
	if ref := m.selected(); ref == nil || ref.id != "A-2" {
		t.Errorf("after the last row left the cursor is on %+v, want A-2", ref)
	}
}
