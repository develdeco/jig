package tui

import (
	"strings"
	"testing"
)

// stack is the screens on the way in, as their names.
func stack(g *rig) []string {
	var out []string
	for _, e := range g.m.nav {
		out = append(out, e.screen.String())
	}
	return out
}

// TestStepKeysReplaceAboveTheTicket: 1 to 5 open the ticket's steps from any
// screen of the ticket as a child of its own screen, so esc from any step lands
// on the ticket; the key of the screen already showing does nothing.
func TestStepKeysReplaceAboveTheTicket(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...)
	want := func(label string, names ...string) {
		t.Helper()
		if got := stack(g); strings.Join(got, ">") != strings.Join(names, ">") {
			t.Fatalf("%s: the stack is %v, want %v", label, got, names)
		}
	}
	want("start", "main", "ticket")

	g.press("3")
	want("3 from the ticket", "main", "ticket", "review")
	g.press("2")
	want("2 from the review", "main", "ticket", "build")
	if head := strings.Split(g.view(), "\n")[0]; !strings.Contains(head, "› 2 build") {
		t.Errorf("the header does not name the build: %s", head)
	}
	g.press("2")
	want("2 on the build screen", "main", "ticket", "build")

	// Into a slice, and from the slice to the review: the slice and the build
	// are replaced, not retraced.
	g.press("enter")
	want("enter on a slice", "main", "ticket", "build", "slice")
	g.press("3")
	want("3 from the slice", "main", "ticket", "review")
	g.press("esc")
	want("esc from the review", "main", "ticket")

	// From the land screen to the brief, and each key to its screen.
	g.press("5")
	want("5", "main", "ticket", "land")
	g.press("1")
	want("1 from the land screen", "main", "ticket", "brief")
	for key, screen := range map[string]string{"1": "brief", "2": "build", "3": "review", "4": "publish", "5": "land"} {
		g.press(key)
		if g.m.Screen() != screen || len(g.m.nav) != 3 {
			t.Errorf("%s opened %s at depth %d, want %s at 3", key, g.m.Screen(), len(g.m.nav), screen)
		}
	}

	// A ticket reached from the project's list is the same: esc lands on it.
	m := mainRig(t)
	m.press("enter", "4", "esc")
	if m.m.Screen() != "ticket" || len(m.m.nav) != 2 {
		t.Errorf("esc from a step opened from the list went to %s at depth %d", m.m.Screen(), len(m.m.nav))
	}
}

// TestStepKeyReasons: the step keys have a reason in the table where a ticket
// has no steps to open, and the notice says it: a ticket that does not exist
// yet, one the roster has not read, a multi-task.
func TestStepKeyReasons(t *testing.T) {
	t.Parallel()
	g := awaitRig(t)
	g.press("2")
	if g.m.Screen() != "ticket" || g.m.notice != "SHOP-1 does not exist yet" {
		t.Errorf("a ticket that does not exist yet: %s, notice %q", g.m.Screen(), g.m.notice)
	}
	g.press("?")
	if v := readWords(g.view()); !strings.Contains(v, "1-5 steps not now: SHOP-1 does not exist yet") {
		t.Errorf("? does not say why:\n%s", g.view())
	}

	r := can1(t, buildUnder())
	r.m.w.tickets["JIG-2"].digest = nil
	r.press("3")
	if r.m.Screen() != "ticket" || r.m.notice != readingText {
		t.Errorf("a ticket nothing has been read of: %s, notice %q", r.m.Screen(), r.m.notice)
	}

	p := planRig(t, planSpec{multi: true})
	onRow(t, p, "SHOP-7")
	p.press("enter", "4")
	if p.m.Screen() != "ticket" || p.m.top().ticket != "SHOP-7" || !strings.Contains(p.m.notice, "multi-task") {
		t.Errorf("a multi-task: %s of %q, notice %q", p.m.Screen(), p.m.top().ticket, p.m.notice)
	}
	if hint := footerKeys(stripANSI(p.view())); strings.Contains(hint, "1-5 steps") {
		t.Errorf("a multi-task's hint line offers the steps:\n%s", hint)
	}
}

// TestHGoesToMain: h cuts the stack to the project's list from any screen, and
// has nothing to do on it.
func TestHGoesToMain(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...)
	g.press("2", "enter")
	if g.m.Screen() != "slice" {
		t.Fatalf("on %s, want a slice", g.m.Screen())
	}
	if hint := footerKeys(g.view()); !strings.Contains(hint, "h main") {
		t.Errorf("the slice's hint line lacks h:\n%s", hint)
	}
	g.press("h")
	if g.m.Screen() != "main" || len(g.m.nav) != 1 {
		t.Errorf("h went to %s at depth %d", g.m.Screen(), len(g.m.nav))
	}
	before := g.view()
	g.press("h")
	if g.view() != before || g.m.notice != "" {
		t.Errorf("h on the project's list did something: notice %q", g.m.notice)
	}
	if hint := footerKeys(before); strings.Contains(hint, "h main") {
		t.Errorf("the project's hint line offers h:\n%s", hint)
	}
}

// TestCOpensTheTicketsChart: c goes to the issue's plan of a task, else the
// first chart that lists the ticket, and a ticket in no chart says so.
func TestCOpensTheTicketsChart(t *testing.T) {
	t.Parallel()

	// A task of an issue: its plan.
	p := planRig(t, planSpec{loose: true})
	onRow(t, p, "SHOP-2")
	p.press("enter", "c")
	if p.m.Screen() != "chart" || p.m.top().chart != "repeat" {
		t.Errorf("c on an issue's task opened %s of %q", p.m.Screen(), p.m.top().chart)
	}

	// A ticket of a chart that plans no issue: that chart.
	g := chartRig(t)
	onRow(t, g, "JIG-4")
	g.press("enter")
	if g.m.Screen() != "ticket" || g.m.top().ticket != "JIG-4" {
		t.Fatalf("enter opened %s of %q", g.m.Screen(), g.m.top().ticket)
	}
	if hint := footerKeys(g.view()); !strings.Contains(hint, "c chart") {
		t.Errorf("the hint line of a chart's ticket lacks c chart:\n%s", hint)
	}
	g.press("c")
	if g.m.Screen() != "chart" || g.m.top().chart != "checkout" {
		t.Fatalf("c opened %s of %q, want the chart checkout", g.m.Screen(), g.m.top().chart)
	}
	g.press("esc")
	if g.m.Screen() != "ticket" || g.m.top().ticket != "JIG-4" {
		t.Errorf("esc went to %s of %q", g.m.Screen(), g.m.top().ticket)
	}

	// A ticket in no chart.
	n := mainRig(t)
	n.press("enter")
	if hint := footerKeys(n.view()); strings.Contains(hint, "c chart") {
		t.Errorf("a ticket in no chart offers c:\n%s", hint)
	}
	n.press("c")
	if n.m.Screen() != "ticket" || n.m.notice != "JIG-1 is in no chart" {
		t.Errorf("c on a ticket in no chart: %s, notice %q", n.m.Screen(), n.m.notice)
	}
}

// TestAScreenKeepsItsCursor: a screen the person leaves keeps its cursor and its
// follow flag for the session, and any move that opens it again starts there,
// inside the rows it has now.
func TestAScreenKeepsItsCursor(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...)

	// The build screen: the cursor on the third slice, then another step and back.
	g.press("2", "down", "down")
	sel := g.m.top().sel
	if sel != 2 {
		t.Fatalf("the cursor is on row %d, want 2", sel)
	}
	g.press("3")
	if g.m.top().sel == sel && g.m.Screen() == "build" {
		t.Fatalf("3 did not leave the build screen")
	}
	g.press("2")
	if g.m.Screen() != "build" || g.m.top().sel != sel {
		t.Errorf("the build screen opened with its cursor on %d, want %d", g.m.top().sel, sel)
	}

	// A slice: left by esc, entered again by enter on its row.
	g.press("enter")
	g.press("down")
	slice := g.m.top().sel
	g.press("esc", "enter")
	if g.m.Screen() != "slice" || g.m.top().sel != slice {
		t.Errorf("the slice screen opened with its cursor on %d, want %d", g.m.top().sel, slice)
	}

	// The ticket's screen: the cursor the person moved is kept by h and enter,
	// and so is the following of the current step it ended.
	g.press("esc", "esc")
	g.press("down")
	if e := g.m.top(); e.follow {
		t.Fatalf("a moved cursor still follows the current step")
	}
	want := g.m.top().sel
	g.press("h", "enter")
	if e := g.m.top(); e.screen != screenTicket || e.sel != want || e.follow {
		t.Errorf("the ticket screen opened with row %d following %v, want row %d not following", e.sel, e.follow, want)
	}

	// Kept inside the rows it has now: a cursor past the end is on the last row.
	for k := range g.m.kept {
		if k.screen == screenBuild {
			// A screen kept with its cursor on a row that has since gone.
			e := g.m.kept[k]
			e.sel, e.seenSel, e.paneCurs = 99, 99, map[string]paneCursor{e.paneFocus: {id: rowIdentity{kind: rowSlice, id: "gone"}, idx: 99, set: true}}
			g.m.kept[k] = e
		}
	}
	g.press("2")
	if n := len(g.m.page().refs()); g.m.top().sel < 0 || g.m.top().sel >= n || g.m.selected() == nil {
		t.Errorf("a kept cursor past the end is on row %d of %d, want a row it has", g.m.top().sel, n)
	}
}
