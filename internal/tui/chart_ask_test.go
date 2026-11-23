package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/journal"
)

// askChartRig is the chart screen of a run whose ticket JIG-5 has the asks
// given, on a screen of w by h.
func askChartRig(t *testing.T, w, h int, asks ...[3]string) *rig {
	t.Helper()
	g := newRig(t, "")
	lines, status := chartTickets()
	status["JIG-5"] = relabelled(askStatus(asks...), "JIG-2", "JIG-5")
	answerChart(g, chartRoster(checkoutListing()), "checkout", checkoutJSON(), status)
	g.send(tea.WindowSizeMsg{Width: w, Height: h})
	g.feed(lines...)
	onChart(g)
	if got := g.m.targetTicket(); got != "JIG-5" {
		t.Fatalf("the cursor is on %q, want JIG-5:\n%s", got, g.view())
	}
	return g
}

func TestChartDetailShowsTheAskWhole(t *testing.T) {
	t.Parallel()
	g := askChartRig(t, 100, 70, askOne)
	panel := strings.Join(detailPanelOf(t, g.view()), "\n")
	if !strings.Contains(panel, "ask      r1-f1 · high") {
		t.Errorf("the Detail panel does not label the ask with its id and risk:\n%s", panel)
	}
	if !strings.Contains(readWords(strings.ReplaceAll(panel, "│", "")), readWords(wholeAskText)) {
		t.Errorf("the Detail panel does not hold the ask's text whole:\n%s", panel)
	}
	if strings.Contains(panel, "…") {
		t.Errorf("the Detail panel cuts the ask:\n%s", panel)
	}
	// The text is wrapped at the panel's width, over three lines or more.
	if n := strings.Count(panel, "\n") - 2; n < 4 {
		t.Errorf("the ask's text is %d lines under its label, want it wrapped over several:\n%s", n, panel)
	}
	for _, l := range strings.Split(g.view(), "\n") {
		if width(l) > 100 {
			t.Errorf("the line %q is wider than the screen", l)
		}
	}

	// Several asks each have their text, and a decided one says so.
	g = askChartRig(t, 100, 70, askOne, askTwo)
	panel = readWords(strings.ReplaceAll(strings.Join(detailPanelOf(t, g.view()), "\n"), "│", ""))
	for _, want := range []string{readWords(wholeAskText), "r1-f2 · high Name shadows import"} {
		if !strings.Contains(panel, want) {
			t.Errorf("the Detail panel of two asks lacks %q:\n%s", want, panel)
		}
	}

	kept := askChartRig(t, 100, 70, askOne, askTwo)
	kept.feed(jl(at(12, 0), "JIG-5", "", journal.EventTriage, with("s-person", "", "keep", "", "r1-f1", "person")))
	if panel := readWords(strings.ReplaceAll(strings.Join(detailPanelOf(t, kept.view()), "\n"), "│", "")); !strings.Contains(panel, "r1-f1 · high · kept A failed write") {
		t.Errorf("the Detail panel does not say r1-f1 was kept:\n%s", panel)
	}

	// A decided ask says what was decided; with every ask decided there are no
	// ask rows, as on the ticket screen.
	g.feed(jl(at(12, 0), "JIG-5", "", journal.EventTriage, with("s-person", "", "dismiss", "", "r1-f2", "person")))
	panel = readWords(strings.ReplaceAll(strings.Join(detailPanelOf(t, g.view()), "\n"), "│", ""))
	if !strings.Contains(panel, "r1-f2 · high · dismissed Name shadows import") || strings.Contains(panel, "r1-f1 · high · ") {
		t.Errorf("the Detail panel does not say r1-f2 was dismissed and r1-f1 was not:\n%s", panel)
	}
	g.feed(jl(at(12, 5), "JIG-5", "", journal.EventTriage, with("s-person", "", "keep", "", "r1-f1", "person")))
	if panel := strings.Join(detailPanelOf(t, g.view()), "\n"); strings.Contains(panel, "ask ") {
		t.Errorf("a ticket with every ask decided has ask rows:\n%s", panel)
	}

	// The cursor on a ticket with no ask draws the panel as before.
	g.press("up")
	if got := g.m.targetTicket(); got == "JIG-5" {
		t.Fatalf("up did not move the cursor off JIG-5")
	}
	if panel := strings.Join(detailPanelOf(t, g.view()), "\n"); strings.Contains(panel, "ask ") || strings.Contains(panel, "Name shadows") {
		t.Errorf("a ticket with no ask has ask rows in its Detail panel:\n%s", panel)
	}
}

// TestChartDetailKeepsAnAskWhenTheScreenIsShort: a short screen gives up slice
// rows and the map before it gives up a line of an ask.
func TestChartDetailKeepsAnAskWhenTheScreenIsShort(t *testing.T) {
	t.Parallel()
	rows := []runRow{
		{"slice", "s1", 1, ""}, {"", "s2", 1, ""}, {"hold", "h", 0, ""},
		{"ask", "r1-f1 · high", 0, "r1-f1"}, {"", "line one", 0, "r1-f1"}, {"", "line two", 0, "r1-f1"},
	}
	for n, want := range map[int]string{
		4: "s1 s2 h r1-f1 · high line one line two",
		2: "s1 h r1-f1 · high line one line two",
		1: "h r1-f1 · high line one line two",
		0: "h r1-f1 · high line one line two",
	} {
		var got []string
		for _, r := range trimRuns(rows, n) {
			got = append(got, r.text)
		}
		if strings.Join(got, " ") != want {
			t.Errorf("trimRuns(rows, %d) = %q, want %q", n, strings.Join(got, " "), want)
		}
	}
}

func TestShortenRunsGivesBackOnlyWhatIsCuttable(t *testing.T) {
	t.Parallel()
	rows := []runRow{
		{"slice", "s1", 1, ""}, {"", "s2", 1, ""}, {"hold", "h", 0, ""},
		{"ask", "r1-f1 · high", 0, "r1-f1"}, {"", "line one", 0, "r1-f1"}, {"", "line two", 0, "r1-f1"},
	}
	for short, want := range map[int]string{
		1: "s1 h r1-f1 · high line one line two:1",
		2: "h r1-f1 · high line one line two:2",
		9: "h r1-f1 · high line one line two:2",
		0: "s1 s2 h r1-f1 · high line one line two:0",
	} {
		got, cut := shortenRuns(rows, short)
		var texts []string
		for _, r := range got {
			texts = append(texts, r.text)
		}
		if line := strings.Join(texts, " ") + ":" + string(rune('0'+cut)); line != want {
			t.Errorf("shortenRuns(rows, %d) = %q, want %q", short, line, want)
		}
	}
}

func TestChartKeepNeedsTheAskDrawnWhole(t *testing.T) {
	t.Parallel()
	// Drawn whole in the Detail panel, k keeps the ask the panel shows.
	g := askChartRig(t, 100, 70, askOne)
	g.press("k")
	if got := triaged(g); len(got) != 1 || !strings.Contains(got[0], "triage JIG-5 r1-f1 keep") {
		t.Errorf("k on the chart with the ask drawn whole ran %q", got)
	}

	// At 100 by 24 the Detail panel is the last that fits whole; at 100 by 18
	// the window cuts it after its state row, and k runs no triage and says
	// which ticket to open.
	g = askChartRig(t, 100, 24, askOne)
	g.press("k")
	if got := triaged(g); len(got) != 1 {
		t.Errorf("k at 100 by 24 ran %q", got)
	}
	g = askChartRig(t, 100, 18, askOne)
	if whole, _ := g.m.askDrawn("r1-f1", g.m.notice); whole {
		t.Fatalf("the test needs the window to cut the ask at 100 by 18:\n%s", g.view())
	}
	g.press("k")
	if got := triaged(g); len(got) != 0 {
		t.Errorf("k decided an ask the chart did not draw whole: %q", got)
	}
	if want := "ask r1-f1 is not drawn whole on this screen: make the screen taller, or open JIG-5 to read it"; g.m.notice != want {
		t.Errorf("the notice = %q, want %q", g.m.notice, want)
	}
}

// TestChartDetailGivesUpSliceRowsBeforeAnAsk: on a ticket with a slice being
// built, a hold and an ask, a screen short of rows drops the slice row first and
// the hold next, and the ask's lines stay until the window itself cuts them.
func TestChartDetailGivesUpSliceRowsBeforeAnAsk(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		height      int
		slice, hold bool
		whole       bool
	}{
		{28, true, true, true},
		{26, false, true, true},
		{18, false, true, true},
		{16, false, true, false},
	} {
		g := heldChartRig(t)
		g.send(tea.WindowSizeMsg{Width: 100, Height: c.height})
		onChart(g)
		g.m.w.tickets["JIG-5"].status.Asks = []statusAsk{{ID: "r1-f1", Risk: "high", Where: "a.go:1", Title: longAskTitle, Why: longAskWhy}}
		view := g.view()
		if got := strings.Contains(view, "slice    s2 · claimed"); got != c.slice {
			t.Errorf("at height %d the slice row is drawn: %v, want %v:\n%s", c.height, got, c.slice, view)
		}
		if got := strings.Contains(view, "hold     ⏸ agent 4f2a91c0"); got != c.hold {
			t.Errorf("at height %d the hold row is drawn: %v, want %v:\n%s", c.height, got, c.hold, view)
		}
		if got, _ := g.m.askDrawn("r1-f1", g.m.notice); got != c.whole {
			t.Errorf("at height %d the ask is drawn whole: %v, want %v:\n%s", c.height, got, c.whole, view)
		}
	}
}

func TestChartKeepWithSeveralAsksKeepsTodaysNotice(t *testing.T) {
	t.Parallel()
	g := askChartRig(t, 100, 70, askOne, askTwo)
	g.press("k")
	if got := triaged(g); len(got) != 0 || !strings.Contains(g.m.notice, "several asks are waiting") {
		t.Errorf("k with two asks open ran %q, notice %q", got, g.m.notice)
	}
}
