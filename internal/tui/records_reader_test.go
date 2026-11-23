package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// manyProbesTwoAsks is a review status with a round that took 12 caught probes
// and two asks waiting after them.
func manyProbesTwoAsks() string {
	var probes [][]string
	for i := 1; i <= 12; i++ {
		probes = append(probes, []string{"1", "caught", "alpha/a.go:" + strconv.Itoa(i), "seam", "mutation " + strconv.Itoa(i), "go test ./alpha"})
	}
	out := ledgerStatus([][]string{ledgerRound("1", "12", "0", "0", "0")}, axi.Table("probes", probeColumns, probes))
	out = strings.Replace(out, "outstanding_asks[1]", "outstanding_asks[2]", 1)
	first := "  r1-f1,high,\"alpha/a.go:3\",Unchecked error,the fix budget is reached\n"
	if !strings.Contains(out, first) {
		panic("the status has no first ask row to follow")
	}
	return strings.Replace(out, first, first+"  r1-f2,low,\"beta/b.go:9\",Name shadows import,-\n", 1)
}

// body is the lines of a screen between its header and its keys, without the
// cursor's marker.
func body(view string) []string {
	lines := strings.Split(view, "\n")
	var out []string
	for _, l := range lines[2 : len(lines)-2] {
		out = append(out, strings.TrimSpace(strings.TrimPrefix(strings.TrimLeft(l, " "), "▸")))
	}
	return out
}

// TestReviewPageOpensAtItsRoundsWithManyProbes: a page with many probes and two
// asks after them opens at its top, so the rounds are read first; down reads on
// a line at a time, from whatever row the cursor is on, until the cursor shows;
// and home, from any row, and up come back to the rounds heading.
func TestReviewPageOpensAtItsRoundsWithManyProbes(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", manyProbesTwoAsks(), 0)
	g.feed(jig2Mid()...).press("enter")
	view := g.view()
	if !hasLine(view, "rounds") || !strings.Contains(view, "12 probes") {
		t.Fatalf("the page does not open at its rounds:\n%s", view)
	}
	// Each down shows the next line: no probe is skipped.
	prev := body(view)
	for i := 0; i < 100 && !strings.Contains(view, "▸"); i++ {
		g.press("down")
		view = g.view()
		cur := body(view)
		if !strings.Contains(view, "▸") && cur[0] != prev[1] {
			t.Fatalf("down moved the page by more than a line (%q, then %q):\n%s", prev[1], cur[0], view)
		}
		prev = cur
	}
	if !strings.Contains(view, "▸") {
		t.Fatalf("reading down never reached the cursor:\n%s", view)
	}
	// The page keeps coming down until it is back at the cursor's window, and
	// the next down moves the cursor to the second ask.
	for i := 0; i < 100 && !strings.Contains(view, "▸ ! low"); i++ {
		g.press("down")
		view = g.view()
	}
	if !strings.Contains(view, "▸ ! low") {
		t.Fatalf("down from the cursor's window did not move it to the second ask:\n%s", view)
	}
	g.press("home")
	if view := g.view(); !hasLine(view, "rounds") {
		t.Errorf("home from the second row does not reach the rounds heading:\n%s", view)
	}
	g.press("end")
	if view := g.view(); !strings.Contains(view, "▸ ! low") {
		t.Errorf("end does not reach the last row:\n%s", view)
	}
	for i := 0; i < 100 && !hasLine(g.view(), "rounds"); i++ {
		g.press("up")
	}
	if view := g.view(); !hasLine(view, "rounds") {
		t.Errorf("up does not reach the rounds heading:\n%s", view)
	}
}

// TestReviewPageKeysActOnlyOnAShownRow: the keys that decide the row under the
// cursor act on a row the window shows with its marker, never on one it hides.
func TestReviewPageKeysActOnlyOnAShownRow(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", manyProbesTwoAsks(), 0)
	g.feed(jig2Mid()...).press("enter")
	if view := g.view(); strings.Contains(view, "▸") {
		t.Fatalf("the cursor's row is shown on the opened page, the test needs it hidden:\n%s", view)
	}
	triages := func() int {
		n := 0
		for _, l := range g.r.lines() {
			if strings.HasPrefix(l, "triage ") {
				n++
			}
		}
		return n
	}
	g.press("d", "k")
	if n := triages(); n != 0 {
		t.Errorf("d and k decided a row the screen did not show: %q", g.r.lines())
	}
	for i := 0; i < 100 && !strings.Contains(g.view(), "▸"); i++ {
		g.press("down")
	}
	g.press("d")
	if got := g.r.lines(); triages() != 1 || !strings.Contains(got[len(got)-1], "triage JIG-2 r1-f1 dismiss") {
		t.Errorf("d on the shown row did not triage it: %q", got)
	}
}

// TestProbeRowKeepsItsRoundAtNarrowWidth: a long path gives way in a probe's
// row at 80 columns, never the round or the verdict.
func TestProbeRowKeepsItsRoundAtNarrowWidth(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	long := "internal/some/very/deeply/nested/package/with/a/long/name/and_a_long_file_name_too.go:1234"
	g.r.answer("status JIG-2 --store /s", ledgerStatus([][]string{ledgerRound("1", "2", "1", "0", "0"), ledgerRound("2", "1", "0", "0", "0")},
		axi.Table("probes", probeColumns, [][]string{
			{"1", "survived", long, "s", "m", "c"},
			{"1", "caught", long + "5", "s", "m", "c"},
			{"2", "caught", long, "s", "m", "c"},
		})), 0)
	g.feed(jig2Mid()...).press("enter")
	seen := 0
	for _, l := range pageLines(g) {
		if strings.Contains(l, "1234") && strings.Contains(l, "round 1") {
			seen++
			if w := width(l) + 2; w > 80 {
				t.Errorf("a probe row is %d cells wide, over 80: %q", w, l)
			}
			if !strings.HasSuffix(l, "survived · caught in round 2") && !strings.HasSuffix(l, "caught") {
				t.Errorf("a probe row lost its verdict: %q", l)
			}
		}
	}
	if seen != 2 {
		t.Errorf("saw %d probe rows of round 1, want 2", seen)
	}
}

// hasLine says whether a screen has a line that is exactly text.
func hasLine(view, text string) bool {
	for _, l := range strings.Split(view, "\n") {
		if strings.TrimSpace(l) == text {
			return true
		}
	}
	return false
}

// pageLines are the lines of the page on top.
func pageLines(g *rig) []string {
	var out []string
	for _, l := range g.m.page().lines {
		out = append(out, l.text)
	}
	return out
}
