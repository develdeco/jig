package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
)

// subjectCard is the ticket of longCard whose slice under way has a commit with
// a subject of n words.
func subjectCard(t *testing.T, width, height, n int) *rig {
	t.Helper()
	status := strings.Join([]string{"ticket: SHOP-1", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"clamp", "building", "1", "-", "-", "Clamp negative quantities"},
		}), "questions: none"}, "\n") + "\n"
	g := newRig(t, "SHOP-1")
	g.r.answer("status SHOP-1 --store /s", status, 0)
	g.send(tea.WindowSizeMsg{Width: width, Height: height})
	g.feed(
		ll(at(0, 0), "", journal.EventSessionStart, func(l *journal.Line) { l.Session, l.Harness, l.Model = "s-b", "claude", "claude-sonnet-5" }),
		jl(at(2, 0), "SHOP-1", "clamp", journal.EventClaim, with("s-b", "", "", "", "", "")),
		jl(at(2, 10), "SHOP-1", "clamp", journal.EventVerifyPending, with("s-b", sha('b'), "", cutTitle(n), "", "")),
	)
	return g
}

// TestAnOpenSubstepsOneRowFieldsRegisterTheirCut: a field of the open sub-step
// that is wider than its block (a commit's subject) ends in a mark, and space on
// the sub-step lays the field out whole.
func TestAnOpenSubstepsOneRowFieldsRegisterTheirCut(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{100, 50}, {80, 50}} {
		g := subjectCard(t, size[0], size[1], 30)
		g.tabTo("Build")
		before := stripANSI(g.view())
		commit := between(paneBlock(t, before, "Build"), "commit", "now")
		if !strings.Contains(commit, "…") || hasWordsInOrder(commit, cutTitle(30)) {
			t.Fatalf("%dx%d: the commit field is not cut:\n%s", size[0], size[1], before)
		}
		everyCutHasAHolder(t, "the open sub-step", subjectCard(t, size[0], size[1], 30).tabTo("Build"))
		g.press("space")
		open := stripANSI(g.view())
		if commit := between(paneBlock(t, open, "Build"), "commit", "now"); strings.Contains(commit, "…") || !hasWordsInOrder(commit, cutTitle(30)) {
			t.Errorf("%dx%d: space did not lay the commit field out whole:\n%s", size[0], size[1], open)
		}
	}
}

// longDestinationChart is the chart screen whose map has a destination of n
// words.
func longDestinationChart(t *testing.T, w, h, n int) *rig {
	t.Helper()
	g := newRig(t, "")
	lines, status := chartTickets()
	mod := func(v map[string]any) {
		mp := checkoutMap()
		mp["destination"] = []string{cutTitle(n)}
		v["map"] = mp
	}
	answerChart(g, chartRoster(checkoutListing()), "checkout", checkoutJSON(mod), status)
	g.feed(lines...)
	g.send(tea.WindowSizeMsg{Width: w, Height: h})
	return g.openChart("checkout")
}

// TestTheMapsCutDestinationExpands: a destination longer than the Map pane's two
// rows ends in a mark; space on the focused pane shows it whole and folds back.
func TestTheMapsCutDestinationExpands(t *testing.T) {
	t.Parallel()
	g := longDestinationChart(t, 100, 60, 70)
	g.tabTo("Map")
	before := stripANSI(g.view())
	if m := paneBlock(t, before, "Map"); !strings.Contains(m, "…") || hasWordsInOrder(m, cutTitle(70)) {
		t.Fatalf("the destination is not cut:\n%s", before)
	}
	everyCutHasAHolder(t, "the chart's map", longDestinationChart(t, 100, 60, 70).tabTo("Map"))
	g.press("space")
	open := stripANSI(g.view())
	if m := paneBlock(t, open, "Map"); strings.Contains(m, "…") || !hasWordsInOrder(m, cutTitle(70)) {
		t.Errorf("space did not show the destination whole:\n%s", open)
	}
	g.press("space")
	if folded := stripANSI(g.view()); folded != before {
		t.Errorf("folding does not give the frame back:\n--- before\n%s\n--- after\n%s", before, folded)
	}
}

// TestAClippedBodyLineWiderThanThePaneKeepsItsMark: a clipped line of a text,
// which carries the mark of its clip, is wider than the pane and wraps: the mark
// is on the last row it wraps to, where the ellipsis lands, and the pane that
// holds it expands it whole.
func TestAClippedBodyLineWiderThanThePaneKeepsItsMark(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.send(tea.WindowSizeMsg{Width: 80, Height: 30})
	text := "    " + cutTitle(30) + " …"
	var p page
	p.boxTop("Checklist", "")
	p.boxAdd("a step")
	p.boxUnder(text)
	p.lines[len(p.lines)-1].cuts = []cutMark{{x: width(text) - 1, value: "the whole body"}}
	p.boxEnd()
	wrapped := g.m.wrapLong(p)
	var marks []cutMark
	rows := 0
	for _, l := range wrapped.lines {
		if l.under {
			rows++
		}
		marks = append(marks, l.cuts...)
	}
	if rows < 2 {
		t.Fatalf("the clipped line did not wrap: %d rows", rows)
	}
	last := wrapped.lines[len(wrapped.lines)-2]
	if len(marks) != 1 || len(last.cuts) != 1 || last.cuts[0].value != "the whole body" || last.cuts[0].x != width(last.text)-1 {
		t.Errorf("the clip's mark is not on the last row it wraps to: %+v on %q", marks, last.text)
	}
	if cells := []rune(stripANSI(last.text)); cells[last.cuts[0].x] != '…' {
		t.Errorf("the mark does not sit on the ellipsis: %q", last.text)
	}
}

// TestARoundsCutWhoLineRegistersItsMark: a round's row is cut by its width; every
// cut of the Rounds list is a mark held by its row, and the who line the row
// once carried is the Detail's, which the cursor row reads whole.
func TestARoundsCutWhoLineRegistersItsMark(t *testing.T) {
	t.Parallel()
	g := reviewOf(t, 60, 50)
	g.tabTo("Rounds")
	frame := stripANSI(g.view())
	if !strings.Contains(frame, "independent") {
		t.Errorf("the round's detail does not say who reviewed it:\n%s", frame)
	}
	everyCutHasAHolder(t, "the review's rounds", reviewOf(t, 60, 50).tabTo("Rounds"))
}

// TestALayoutIsAFixedPoint: a screen laid out once is laid out the same again:
// the keys' height never reads what the body cuts, so frames drawn with nothing
// in between are equal, an idle tick draws the same frame, and space expands the
// cut line.
func TestALayoutIsAFixedPoint(t *testing.T) {
	t.Parallel()
	g := longCard(t, 80, 24)
	for i := 0; i < 40; i++ {
		g.feed(toolOn("SHOP-1", at(3, i), "s-b", "go test ./cart/... "+cutTitle(30), exit(0)))
	}
	g.tabTo("Activity")
	first := stripANSI(g.view())
	for i := 0; i < 4; i++ {
		g.send(idleMsg{})
		if again := stripANSI(g.view()); again != first {
			t.Fatalf("an idle tick changed the frame:\n--- first\n%s\n--- again\n%s", first, again)
		}
	}
	if !strings.Contains(first, "space expand") {
		t.Errorf("the keys do not list space:\n%s", first)
	}
	if !strings.Contains(paneBlock(t, first, "Activity"), "…") {
		t.Fatalf("no line of the Activity pane is cut:\n%s", first)
	}
	g.press("space")
	open := stripANSI(g.view())
	if open == first || expandedIn(g) != 1 {
		t.Errorf("space did not expand the cut line:\n%s", open)
	}
	g.press("space")
	if folded := stripANSI(g.view()); folded != first {
		t.Errorf("folding does not give the frame back:\n--- first\n%s\n--- folded\n%s", first, folded)
	}
	// With nothing cut under the cursor space says so, and the keys stay as they
	// were: the same lines, whatever the body cuts.
	keysOf := func(frame string) string { return frame[strings.LastIndex(frame, "───"):] }
	for i := 0; i < 60 && g.m.keysOn.cut; i++ {
		g.press("down")
	}
	if g.m.keysOn.cut {
		t.Fatalf("every line of the pane is cut:\n%s", g.view())
	}
	was := keysOf(stripANSI(g.view()))
	g.press("space")
	if g.m.notice != "nothing is cut here" {
		t.Errorf("space with nothing cut says %q", g.m.notice)
	}
	if now := keysOf(stripANSI(g.view())); now != was {
		t.Errorf("the keys changed with what the body cuts:\n--- was\n%s\n--- now\n%s", was, now)
	}
}

// TestAnExpandedRowStaysInsideItsPane: an expanded row is read by lines in the
// room its pane has: the panes around it stay where they were and the screen
// leaves no empty rows, at 60x20 and at 80x24.
func TestAnExpandedRowStaysInsideItsPane(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{60, 20}, {80, 24}} {
		g := longCard(t, size[0], size[1])
		for i := 0; i < 12; i++ {
			g.feed(toolOn("SHOP-1", at(3, i), "s-b", "git commit -m "+cutTitle(60), exit(0)))
		}
		g.tabTo("Activity")
		before := stripANSI(g.view())
		heightOf := func(frame string) int { return len(panelLines(strings.Split(frame, "\n"), "Activity")) }
		g.press("space")
		open := stripANSI(g.view())
		if !strings.Contains(open, "╭─ Steps") || strings.Contains(open, "↑ ") && !strings.Contains(open, "more line") {
			t.Errorf("%dx%d: expanding pushed a pane off the screen:\n%s", size[0], size[1], open)
		}
		if heightOf(open) != heightOf(before) {
			t.Errorf("%dx%d: the Activity pane grew from %d to %d lines:\n%s", size[0], size[1], heightOf(before), heightOf(open), open)
		}
		if blank := strings.Count(open, "\n\n"); blank > strings.Count(before, "\n\n") {
			t.Errorf("%dx%d: expanding left empty rows:\n%s", size[0], size[1], open)
		}
		// It is read to its end by lines, the cursor staying on it.
		for i := 0; i < 60 && !strings.Contains(paneBlock(t, open, "Activity"), "w060"); i++ {
			g.press("down")
			open = stripANSI(g.view())
		}
		if !strings.Contains(paneBlock(t, open, "Activity"), "w060") {
			t.Errorf("%dx%d: the line is not read to its end:\n%s", size[0], size[1], open)
		}
		g.press("space")
	}
}

// TestEveryCutHasAMarkAndAHolder: on each screen at each size the tests draw,
// every ellipsis that is no word of the TUI's own is a mark, and a click on it
// lays its holder out whole.
func TestEveryCutHasAMarkAndAHolder(t *testing.T) {
	t.Parallel()
	sizes := [][2]int{{60, 20}, {80, 24}, {100, 30}}
	screens := map[string]func(t *testing.T, w, h int) *rig{
		"main": func(t *testing.T, w, h int) *rig { return cutRig(t, w, h, 40).onRow("JIG-1") },
		"chart": func(t *testing.T, w, h int) *rig {
			return sized(longChartRig(t, 40), w, h).openChart("checkout").onRow("JIG-8")
		},
		"ticket":   func(t *testing.T, w, h int) *rig { return subjectCard(t, w, h, 30) },
		"activity": func(t *testing.T, w, h int) *rig { return longCard(t, w, h).tabTo("Activity") },
		"build":    func(t *testing.T, w, h int) *rig { return buildRig(t, w, h, 40) },
		"map":      func(t *testing.T, w, h int) *rig { return longDestinationChart(t, w, h, 70) },
		"slice":    func(t *testing.T, w, h int) *rig { return buildRig(t, w, h, 40).press("enter") },
		// A fog entry is a row of the Tickets pane the cursor cannot stand on: it is
		// never cut, so there is no mark to leave without a holder.
		"fog": func(t *testing.T, w, h int) *rig {
			g := newRig(t, "")
			lines, status := chartTickets()
			fog := func(v map[string]any) {
				for _, e := range v["tickets"].([]map[string]any) {
					if e["id"] == "" {
						e["title"] = cutTitle(42)
					}
				}
			}
			answerChart(g, chartRoster(checkoutListing()), "checkout", checkoutJSON(fog), status)
			g.feed(lines...)
			g.send(tea.WindowSizeMsg{Width: w, Height: h})
			return g.openChart("checkout")
		},
	}
	for name, mk := range screens {
		for _, size := range sizes {
			everyCutHasAHolder(t, fmt.Sprintf("%s %dx%d", name, size[0], size[1]), mk(t, size[0], size[1]))
		}
	}
}
