package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// head is the first line of a frame.
func head(g *rig) string { return strings.SplitN(stripANSI(g.view()), "\n", 2)[0] }

// ticketRows are the ids of the ticket rows of the page of the screen on top,
// in the order it draws them.
func ticketRows(g *rig) []string {
	var out []string
	for _, r := range g.m.page().refs() {
		if r.kind == rowTicket {
			out = append(out, r.id)
		}
	}
	return out
}

// TestSiblingKeysWalkSlices: [ and ] go to the previous and next slice of the
// ticket in its plan order, in place of the slice on top.
func TestSiblingKeysWalkSlices(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...)
	ids := []string{}
	for _, s := range g.m.w.tickets["JIG-1"].status.Slices {
		ids = append(ids, s.ID)
	}
	if len(ids) < 3 {
		t.Fatalf("the fixture has slices %v, want 3 or more", ids)
	}
	g.press("2", "enter")
	if g.m.Screen() != "slice" || g.m.top().slice != ids[0] {
		t.Fatalf("on %s of %q, want the first slice %q", g.m.Screen(), g.m.top().slice, ids[0])
	}
	depth := len(g.m.nav)
	for i := 1; i < len(ids); i++ {
		g.press("]")
		if g.m.Screen() != "slice" || g.m.top().slice != ids[i] || len(g.m.nav) != depth {
			t.Fatalf("] went to %s of %q at depth %d, want slice %q at %d", g.m.Screen(), g.m.top().slice, len(g.m.nav), ids[i], depth)
		}
	}
	g.press("[", "[")
	if g.m.top().slice != ids[len(ids)-3] {
		t.Errorf("[ [ went to %q, want %q", g.m.top().slice, ids[len(ids)-3])
	}
	g.press("esc")
	if g.m.Screen() != "build" {
		t.Errorf("esc from a slice went to %s, want the build screen", g.m.Screen())
	}
}

// TestSiblingKeysWalkTheListItCameFrom: the siblings of a ticket and of every
// step of it are the ticket rows of the list it was opened from, a neighbor
// opens as the screen the person is on, and the list puts its cursor on it so
// esc lands there; a plan's siblings are the chart rows of the project's list.
func TestSiblingKeysWalkTheListItCameFrom(t *testing.T) {
	t.Parallel()

	// From the project's list.
	g := mainRig(t)
	rows := ticketRows(g)
	if len(rows) < 3 {
		t.Fatalf("main lists %v", rows)
	}
	g.press("enter", "3")
	if g.m.Screen() != "review" || g.m.top().ticket != rows[0] {
		t.Fatalf("on %s of %q, want the review of %q", g.m.Screen(), g.m.top().ticket, rows[0])
	}
	g.press("]")
	if g.m.Screen() != "review" || g.m.top().ticket != rows[1] || strings.Join(stack(g), ">") != "main>ticket>review" {
		t.Fatalf("] went to %s of %q, stack %v, want the review of %q", g.m.Screen(), g.m.top().ticket, stack(g), rows[1])
	}
	g.press("]")
	g.press("[")
	if g.m.top().ticket != rows[1] {
		t.Errorf("] then [ went to %q, want %q", g.m.top().ticket, rows[1])
	}
	g.press("2", "]")
	if g.m.Screen() != "build" || g.m.top().ticket != rows[2] {
		t.Errorf("] from the build screen went to %s of %q, want the build of %q", g.m.Screen(), g.m.top().ticket, rows[2])
	}
	g.press("esc", "esc")
	if ref := g.m.selected(); g.m.Screen() != "main" || ref == nil || ref.id != rows[2] {
		t.Errorf("esc esc landed on %s with the cursor on %+v, want main on %q", g.m.Screen(), ref, rows[2])
	}

	// From a plan: its tasks are the siblings.
	p := planRig(t, planSpec{})
	tasks := ticketRows(p)
	if len(tasks) < 3 {
		t.Fatalf("the plan lists %v", tasks)
	}
	onRow(t, p, tasks[1])
	p.press("enter", "]")
	if p.m.Screen() != "ticket" || p.m.top().ticket != tasks[2] {
		t.Fatalf("] from a task went to %s of %q, want %q", p.m.Screen(), p.m.top().ticket, tasks[2])
	}
	p.press("[", "[")
	if p.m.top().ticket != tasks[0] {
		t.Errorf("[ [ went to %q, want %q", p.m.top().ticket, tasks[0])
	}
	p.press("esc")
	if ref := p.m.selected(); p.m.Screen() != "chart" || ref == nil || ref.id != tasks[0] {
		t.Errorf("esc landed on %s with the cursor on %+v, want the plan on %q", p.m.Screen(), ref, tasks[0])
	}

	// A plan's siblings are the chart rows of the project's list.
	s := planMain(t, planSpec{second: true})
	s.press("s") // the list by chart
	// The rows that open the two plans are the issues' own.
	charts := []string{"repeat", "second"}
	onRow(t, s, "SHOP-49")
	s.press("enter")
	if s.m.top().chart != charts[0] {
		t.Fatalf("enter opened %q, want %q", s.m.top().chart, charts[0])
	}
	s.press("]")
	if s.m.Screen() != "chart" || s.m.top().chart != charts[1] || len(s.m.nav) != 2 {
		t.Errorf("] opened %s of %q at depth %d, want the chart %q", s.m.Screen(), s.m.top().chart, len(s.m.nav), charts[1])
	}
	s.press("esc")
	if ref := s.m.selected(); ref == nil || ref.id != "SHOP-50" {
		t.Errorf("esc landed with the cursor on %+v, want SHOP-50", ref)
	}
}

// TestSiblingsEndWithAReason: the first has no previous and the last no next,
// nothing wraps, and a ticket that is no longer in its list says so; the reason
// is the table's, in the notice and in `?`.
func TestSiblingsEndWithAReason(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...)
	g.press("2", "enter")
	first := g.m.top().slice
	g.press("[")
	if g.m.top().slice != first || g.m.notice != "the first slice" {
		t.Errorf("[ on the first slice: %q, notice %q", g.m.top().slice, g.m.notice)
	}
	if hint := footerKeys(g.view()); !strings.Contains(hint, "[ ] sibling") {
		t.Errorf("the hint line lacks the sibling keys:\n%s", hint)
	}
	g.press("end")
	for g.m.notice != "the last slice" && len(g.m.nav) == 4 {
		g.press("]")
		if g.m.notice != "" && g.m.notice != "the last slice" {
			t.Fatalf("notice %q", g.m.notice)
		}
	}
	last := g.m.top().slice
	g.press("]")
	if g.m.top().slice != last || g.m.notice != "the last slice" {
		t.Errorf("] on the last slice: %q, notice %q", g.m.top().slice, g.m.notice)
	}
	g.press("?")
	if v := readWords(g.view()); !strings.Contains(v, "] next slice not now: the last slice") || !strings.Contains(v, "[ previous slice now") {
		t.Errorf("? does not say the ends:\n%s", g.view())
	}
	g.press("esc")

	// The first and last tickets of the project's list.
	m := mainRig(t)
	rows := ticketRows(m)
	m.press("enter", "[")
	if m.m.top().ticket != rows[0] || m.m.notice != "the first ticket" {
		t.Errorf("[ on the first ticket: %q, notice %q", m.m.top().ticket, m.m.notice)
	}
	for range rows {
		m.press("]")
	}
	if m.m.top().ticket != rows[len(rows)-1] || m.m.notice != "the last ticket" {
		t.Errorf("] past the last ticket: %q, notice %q", m.m.top().ticket, m.m.notice)
	}

	// A ticket that is not in its list.
	m.m.top().ticket = "JIG-404"
	m.press("]")
	if !strings.Contains(m.m.notice, "is in no list of this screen") {
		t.Errorf("a ticket not in its list: notice %q", m.m.notice)
	}
}

// TestBreadcrumbNamesThePlace: a screen that has siblings ends its id with its
// place among them; one that has none says nothing added; at a width that cannot
// hold it the place goes before the step.
func TestBreadcrumbNamesThePlace(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...)
	n := 0
	for _, r := range g.m.refsOf(0) {
		if r.kind == rowTicket {
			n++
		}
	}
	if h := head(g); !strings.Contains(h, "› JIG-1 (1 of "+strconv.Itoa(n)+")  Greeting copy") {
		t.Errorf("the ticket's header: %q", h)
	}
	g.press("2")
	if h := head(g); !strings.Contains(h, "› JIG-1 (1 of "+strconv.Itoa(n)+")  Greeting copy › 2 build") {
		t.Errorf("the build's header: %q", h)
	}
	g.press("enter")
	if h := head(g); !strings.Contains(h, "› 2 build › slice a (1 of ") {
		t.Errorf("the slice's header: %q", h)
	}
	g.send(tea.WindowSizeMsg{Width: 52, Height: 24})
	if h := head(g); strings.Contains(h, "of") || !strings.Contains(h, "slice a") {
		t.Errorf("a narrow header keeps the place before the step: %q", h)
	}

	// A plan among charts, and a screen with none.
	s := planMain(t, planSpec{second: true})
	s.press("s")
	if h := head(s); strings.Contains(h, " of ") {
		t.Errorf("the project's list has a place: %q", h)
	}
	onRow(t, s, "SHOP-49")
	s.press("enter")
	if h := head(s); !strings.Contains(h, "› repeat (1 of 2)") {
		t.Errorf("the plan's header: %q", h)
	}

	one := newRig(t, "JIG-1")
	one.feed(jig1()...)
	one.m.w.order = []string{"JIG-1"}
	one.m.w.gen++
	if h := head(one); strings.Contains(h, " of ") {
		t.Errorf("a ticket alone in its list has a place: %q", h)
	}
}
