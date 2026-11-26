package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
)

// manySlices is a status of JIG-1 with n slices, none of them started.
func manySlices(n int) string {
	var rows [][]string
	for i := 1; i <= n; i++ {
		id := "s" + strconv.Itoa(i)
		rows = append(rows, []string{id, "queued", "0", "-", "-", "Goal of slice " + id, "app", "unit", "-"})
	}
	return slicesStatus(rows...)
}

// manySlicesBrief is manySlices with the brief block and its sections.
func manySlicesBrief(n int) string {
	return strings.Replace(manySlices(n), "questions: none\n", "questions: none\n"+
		axi.KV("brief", [][2]string{{"approval", "current"}, {"approved_by", "an agent (as Ada Lovelace)"}, {"identity", "Ada Lovelace"}, {"approved_at", "2026-10-23T09:00:00Z"}, {"provenance", "agent"}})+"\n"+
		axi.Table("brief_sections", []string{"heading", "slices"}, [][]string{{"Goal", "-"}, {"Greeting", "s1"}, {"Farewell", "s2"}, {"Tone", "s3+s4"}})+"\n", 1)
}

// TestADetailBesideItsListIsMarkedWhereTheWindowCutsIt: from 160 columns the list
// and the detail are cut each by its own box, so a list says what it hides of its
// own rows and the detail what it hides of its own, and a detail is never the
// empty rows of a list's padding.
func TestADetailBesideItsListIsMarkedWhereTheWindowCutsIt(t *testing.T) {
	t.Parallel()
	g := tourRig(t, 160, 16)
	g.press("2", "down")
	view := g.view()
	list, detail := view, ""
	if i := strings.Index(view, "╭─ Detail"); i >= 0 {
		detail = view
	}
	if detail == "" {
		t.Fatalf("no detail beside the list:\n%s", view)
	}
	side := sideColumn(view)
	if !strings.Contains(side, "↓ ") || !strings.Contains(side, "goal") {
		t.Errorf("the detail beside the list is cut with no mark of its own, or lost its first field:\n%s", view)
	}
	if strings.Contains(strings.Split(list, "╮  ╭─ Detail")[0], "↓ 4 more") {
		t.Errorf("the list marks rows it has not got:\n%s", view)
	}

	// The brief with the cursor on the last slice: the detail has its goal.
	b := tourRig(t, 160, 20)
	b.press("1", "end")
	if side := sideColumn(b.view()); !strings.Contains(side, "goal") {
		t.Errorf("the detail of the last slice lost its first field:\n%s", b.view())
	}

	// Seventy slices and the cursor on the 56th: the detail has rows, not blanks.
	many := newRig(t, "JIG-1")
	many.r.answer("status JIG-1 --store /s", manySlices(70), 0)
	many.feed(jl(at(0, 1), "JIG-1", "s1", journal.EventClaim, with("s-builder", "", "", "", "", ""))).press("enter").sized(160, 40)
	for i := 0; i < 55; i++ {
		many.press("down")
	}
	view = many.view()
	if !strings.Contains(sideColumn(view), "Goal of slice s56") || !strings.Contains(view, "▸") {
		t.Errorf("with 70 slices the cursor's row or its detail is not drawn:\n%s", view)
	}
}

// TestTheDetailIsDrawnWhateverTheListsLength: a list taller than the screen is
// windowed around its cursor, so the Detail of the cursor's row is drawn with its
// first lines and a mark at least: nine rounds at 100x30, sixteen slices at 80x24
// on the build and the brief.
func TestTheDetailIsDrawnWhateverTheListsLength(t *testing.T) {
	t.Parallel()
	r := newRig(t, "JIG-2")
	r.r.answer("status JIG-2 --store /s", manyProbesTwoAsks(), 0)
	var evs = jig2Mid()
	for n := 2; n <= 9; n++ {
		n := n
		evs = append(evs, jl(at(20+n, 0), "JIG-2", "", journal.EventGateOpen),
			jl(at(20+n, 30), "JIG-2", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = n, "s-reviewer" }))
	}
	r.feed(evs...).press("enter").sized(100, 30)
	if view := r.view(); !strings.Contains(view, "╭─ Detail · ") || !strings.Contains(view, "▸") {
		t.Errorf("nine rounds at 100x30 draw no detail of the cursor's row:\n%s", view)
	}
	r.press("up", "up", "up", "up")
	if view := r.view(); !strings.Contains(view, "╭─ Detail · round") || !strings.Contains(view, "▸") {
		t.Errorf("a round in the middle of nine has no detail:\n%s", view)
	}

	for _, screen := range []string{"build", "brief"} {
		g := newRig(t, "JIG-1")
		g.r.answer("status JIG-1 --store /s", manySlicesBrief(16), 0)
		g.r.answer("brief show JIG-1 --text --store /s", briefTextJIG1(), 0)
		g.feed(jl(at(0, 1), "JIG-1", "s1", journal.EventClaim, with("s-builder", "", "", "", "", ""))).press("enter").sized(80, 24)
		if screen == "brief" {
			g.press("esc", "up", "enter")
		}
		if g.m.Screen() != screen {
			t.Fatalf("on %s, want %s", g.m.Screen(), screen)
		}
		for i := 0; i < 9; i++ {
			g.press("down")
			if view := g.view(); !strings.Contains(view, "╭─ Detail · ") || !strings.Contains(view, "▸") {
				t.Fatalf("%s, row %d of sixteen: no detail or no cursor:\n%s", screen, i+2, view)
			}
		}
	}
}

// TestTheDetailIsDrawnWithTheTailOpenAndAtTheNarrowestSizes: the block of lists
// and detail is never taller than the window has: where the lines above leave it
// too little the window starts at the block, and the detail is drawn on every row
// of the four screens with the tail open at 80x24 and 100x30, at 60x20, and beside
// the list at 160x14.
func TestTheDetailIsDrawnWithTheTailOpenAndAtTheNarrowestSizes(t *testing.T) {
	t.Parallel()
	for _, sc := range []struct{ name, key string }{{"build", "2"}, {"review", "3"}, {"brief", "1"}, {"publish", "4"}} {
		for _, c := range []struct {
			w, h int
			tail bool
		}{{80, 24, true}, {100, 30, true}, {60, 20, false}, {160, 14, false}} {
			g := tourRig(t, c.w, c.h)
			g.press(sc.key)
			if c.tail {
				g.press("t")
			}
			if g.m.Screen() != sc.name {
				t.Fatalf("on %s, want %s", g.m.Screen(), sc.name)
			}
			e := g.m.top()
			e.sel, e.lead, e.follow = 0, 0, false
			g.m.noteCursor()
			for i, n := 0, len(g.m.page().refs()); i < n; i++ {
				view := g.view()
				if !strings.Contains(view, "╭─ Detail · ") || !strings.Contains(view, "▸") {
					t.Fatalf("%s %dx%d tail %v row %d: no detail or no cursor:\n%s", sc.name, c.w, c.h, c.tail, i, view)
				}
				if c.w >= 160 && !strings.Contains(sideColumn(view), "╭─ Detail · ") {
					t.Errorf("%s %dx%d row %d: the detail beside the list begins below its top edge:\n%s", sc.name, c.w, c.h, i, view)
				}
				g.press("down")
			}
		}
	}
	// The brief's last slice at 160x14: the detail beside begins at its first line.
	b := tourRig(t, 160, 14)
	b.press("1", "end")
	if side := sideColumn(b.view()); !strings.Contains(side, "╭─ Detail · ") || !strings.Contains(side, "goal") {
		t.Errorf("the detail of the last slice at 160x14 does not begin at its top edge and first field:\n%s", b.view())
	}
}

// TestTheTailGivesWayToTheListsAndTheDetail: with the tail open the body keeps
// every list's edges, its row under the cursor whole and the first lines of its
// detail; the tail has what is left, and when none is left it is the one line
// that says so.
func TestTheTailGivesWayToTheListsAndTheDetail(t *testing.T) {
	t.Parallel()
	titles := map[string][]string{
		"build": {"Slices"}, "review": {"Rounds", "Asks"}, "brief": {"Sections", "Slices"}, "publish": {"Checklist"},
	}
	for _, sc := range []struct{ name, key string }{{"build", "2"}, {"review", "3"}, {"brief", "1"}, {"publish", "4"}} {
		for _, size := range [][2]int{{60, 20}, {80, 24}, {100, 30}} {
			g := tourRig(t, size[0], size[1])
			g.press(sc.key, "t")
			if g.m.Screen() != sc.name || !g.m.tail {
				t.Fatalf("on %s with the tail %v, want %s open", g.m.Screen(), g.m.tail, sc.name)
			}
			e := g.m.top()
			e.sel, e.lead, e.follow = 0, 0, false
			g.m.noteCursor()
			for i, n := 0, len(g.m.page().refs()); i < n; i++ {
				view := g.view()
				where := func() string {
					return sc.name + " " + strconv.Itoa(size[0]) + "x" + strconv.Itoa(size[1]) + " row " + strconv.Itoa(i)
				}
				if !strings.Contains(view, "╭─ Detail · ") || !strings.Contains(view, "▸") {
					t.Fatalf("%s: no detail or no cursor:\n%s", where(), view)
				}
				for _, title := range titles[sc.name] {
					if !strings.Contains(view, "╭─ "+title+" ") {
						t.Errorf("%s: the %s list is not drawn:\n%s", where(), title, view)
					}
				}
				// The tail is a pane of at least eight rows or the line that says it has none.
				tail, note := strings.Contains(view, "\n  tail ─"), strings.Contains(view, "the tail needs")
				if tail == note {
					t.Errorf("%s: want the tail or its one line, got tail %v and line %v:\n%s", where(), tail, note, view)
				}
				g.press("down")
			}
		}
	}
}

// TestTheTailGivesWayToAFixRowThatWraps: with the tail open at 80x24 a fix slice
// whose finding title wraps to three lines keeps its three-line row and its detail.
func TestTheTailGivesWayToAFixRowThatWraps(t *testing.T) {
	t.Parallel()
	goal := "Fix these gate findings:\n\nalpha/c.go:7 Where the room under the lines above is short, the lists and the Detail are sized past the window, which then drops the Detail"
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", slicesStatus(
		[]string{"a", "green", "1", "-", "-", "Greet", "app", "unit", "-"},
		[]string{"fix-2", "queued", "0", "-", "-", goal, "app", "unit", "2"},
	), 0)
	g.feed(jl(at(0, 1), "JIG-1", "a", journal.EventClaim, with("s-builder", "", "", "", "", ""))).press("enter", "t", "down").sized(80, 24)
	view := g.view()
	if !strings.Contains(view, "╭─ Detail · fix-2") || !strings.Contains(readWords(between(view, "╭─ Slices", "╰")), "round 2 · c.go:7 Where the room under the lines above is short") {
		t.Errorf("the fix row lost its title or its detail is not drawn:\n%s", view)
	}
}

// TestAPartThatCannotFitSaysSoInOneLine: where the body has 13 rows or fewer, the
// Detail is drawn or, below its least room, is one line that names it, the rows
// it needs and the rows it was left, and the lists keep their edges; an open tail
// that has no room says the same of itself. No such line names the body's total.
func TestAPartThatCannotFitSaysSoInOneLine(t *testing.T) {
	t.Parallel()
	if got := partNeeds("tail", 8, 6); got != "the tail needs 8 rows; 6 are left" {
		t.Errorf("partNeeds = %q", got)
	}
	for _, sc := range []struct{ name, key string }{{"build", "2"}, {"review", "3"}, {"brief", "1"}, {"publish", "4"}} {
		for _, size := range [][2]int{{80, 16}, {60, 17}, {80, 18}, {60, 20}} {
			for _, tail := range []bool{false, true} {
				if tail && size[1] < 18 {
					continue // the tail's own line and the lists' marks leave a body this short no room
				}
				g := tourRig(t, size[0], size[1])
				g.press(sc.key)
				if tail {
					g.press("t")
				}
				e := g.m.top()
				e.sel, e.lead, e.follow = 0, 0, false
				g.m.noteCursor()
				for i, n := 0, len(g.m.page().refs()); i < n; i++ {
					view := g.view()
					where := sc.name + " " + strconv.Itoa(size[0]) + "x" + strconv.Itoa(size[1]) + " tail " + strconv.FormatBool(tail) + " row " + strconv.Itoa(i)
					if len(strings.Split(view, "\n")) != size[1] {
						t.Fatalf("%s: the frame is not %d rows:\n%s", where, size[1], view)
					}
					drawn, said := strings.Contains(view, "╭─ Detail · "), strings.Contains(view, "the Detail needs 4 rows; ")
					if drawn == said {
						t.Errorf("%s: the detail is neither drawn nor said to have no room, or both:\n%s", where, view)
					}
					if tail && !strings.Contains(view, "tail ─") && !strings.Contains(view, "the tail needs 8 rows; ") {
						t.Errorf("%s: the open tail is neither drawn nor said to have no room:\n%s", where, view)
					}
					if strings.Contains(view, "this screen has") || strings.Contains(view, "body rows") {
						t.Errorf("%s: a line names the body's total:\n%s", where, view)
					}
					g.press("down")
				}
			}
		}
	}
}

// TestDownMovesTheCursorOnEveryScreenWithRows: down moves the cursor from the
// first row to the last on each phase screen, a row at a time, and the window
// follows it; the page is not scrolled instead.
func TestDownMovesTheCursorOnEveryScreenWithRows(t *testing.T) {
	t.Parallel()
	for _, sc := range []struct{ name, key string }{{"build", "2"}, {"review", "3"}, {"brief", "1"}, {"publish", "4"}} {
		for _, size := range [][2]int{{80, 24}, {100, 30}} {
			g := tourRig(t, size[0], size[1])
			g.press(sc.key, "home")
			if g.m.Screen() != sc.name {
				t.Fatalf("on %s, want %s", g.m.Screen(), sc.name)
			}
			rows := len(g.m.page().refs())
			for i := 1; i < rows; i++ {
				before := g.m.top().sel
				g.press("down")
				if got := g.m.top().sel; got != before+1 {
					t.Fatalf("%s %v: down from row %d put the cursor on %d", sc.name, size, before, got)
				}
				if view := g.view(); !strings.Contains(view, "▸") {
					t.Fatalf("%s %v: the cursor's row is not on the screen after %d downs:\n%s", sc.name, size, i, view)
				}
			}
		}
	}
}

// TestARoundRowCarriesNoProbeCounts: the ledger's probe counts are not in the
// Rounds list; the probes section says them once.
func TestARoundRowCarriesNoProbeCounts(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", probeStatus([][]string{probeRound("1", "3", "1"), probeRound("2", "0", "0")},
		axiProbes()), 0)
	g.feed(append(jig2Mid(),
		jl(at(12, 30), "JIG-2", "", journal.EventGateOpen),
		jl(at(12, 40), "JIG-2", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = 2, "s-reviewer" }),
	)...).press("enter").sized(100, 60)
	rounds := between(g.view(), "╭─ Rounds", "╰")
	if strings.Contains(rounds, "probe") {
		t.Errorf("a round row carries a probe count:\n%s", rounds)
	}
	if !strings.Contains(readWords(g.view()), "3 probes · 2 caught · 1 survived") {
		t.Errorf("the probes section does not say its counts:\n%s", g.view())
	}
}

// TestAFixRowKeepsItsFindingsTitle: a long path is cut before the title of the
// finding in a fix slice's row.
func TestAFixRowKeepsItsFindingsTitle(t *testing.T) {
	t.Parallel()
	goal := "Fix these gate findings:\n\ninternal/some/very/deeply/nested/package/with/a/long/name/detail.go:182 The stacked parent's fallback is no longer reached"
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", slicesStatus(
		[]string{"a", "green", "1", "-", "-", "Greet", "app", "unit", "-"},
		[]string{"fix-2", "queued", "0", "-", "-", goal, "app", "unit", "2"},
	), 0)
	g.feed(jl(at(0, 1), "JIG-1", "a", journal.EventClaim, with("s-builder", "", "", "", "", ""))).press("enter").sized(80, 24)
	list := readWords(between(g.view(), "╭─ Slices", "╰"))
	if !strings.Contains(list, "round 2 · detail.go:182 The stacked parent's fallback is no longer reached") {
		t.Errorf("the fix row lost its finding's title:\n%s", g.view())
	}
}

// TestFindingsShowUnderARoundTheJournalLacks: a round the status names has a row,
// and its findings are in its detail, though the journal has no line of the round.
func TestFindingsShowUnderARoundTheJournalLacks(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	rounds := [][]string{{"11", "findings", "s-reviewer", "claude", "claude-opus-5", "true", "1", "0", "0", "delta", sha('c'), sha('d'), "3", "0", "-", "-"}}
	g.r.answer("status JIG-2 --store /s", reviewStatus(rounds, [][]string{
		{"r11-f1", "11", "fix", "medium", "alpha/c.go:7", "Check the returned error", "open", "the error is dropped", "", ""},
	}), 0)
	g.feed(jl(at(10, 0), "JIG-2", "a", journal.EventClaim, with("s-builder", "", "", "", "", ""))).press("3").sized(100, 40)
	if g.m.Screen() != "review" {
		t.Fatalf("on %s, want review", g.m.Screen())
	}
	view := g.view()
	if !strings.Contains(between(view, "╭─ Rounds", "╰"), "round 11") {
		t.Fatalf("the round the status names has no row:\n%s", view)
	}
	g.focus("11")
	if d := readWords(between(g.view(), "╭─ Detail · round 11", "╰")); !strings.Contains(d, "Check the returned error → to fix") {
		t.Errorf("the round's findings are not under it:\n%s", g.view())
	}
}

// axiProbes is a probes table of three rows of round 1: two caught, one survived.
func axiProbes() string {
	return axi.Table("probes", probeColumns, [][]string{
		{"1", "caught", "alpha/a.go:9", "the parser", "flip the guard", "go test ./alpha"},
		{"1", "caught", "alpha/a.go:20", "the loader", "drop the read", "go test ./alpha"},
		{"1", "survived", "beta/b.go:4", "the greeting", "drop the check", "go test ./beta"},
	})
}
