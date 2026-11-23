package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// TestReviewPageOpensAtItsRoundsWithManyProbes: a page with many probes and an
// ask row after them opens at its top, so the rounds are read first; down reads
// on toward the row, and up and home come back to the rounds heading.
func TestReviewPageOpensAtItsRoundsWithManyProbes(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	var probes [][]string
	for i := 1; i <= 12; i++ {
		probes = append(probes, []string{"1", "caught", "alpha/a.go:" + strconv.Itoa(i), "seam", "mutation " + strconv.Itoa(i), "go test ./alpha"})
	}
	g.r.answer("status JIG-2 --store /s", ledgerStatus([][]string{ledgerRound("1", "12", "0", "0", "0")}, axi.Table("probes", probeColumns, probes)), 0)
	g.feed(jig2Mid()...).press("enter")
	if view := g.view(); !strings.Contains(view, "rounds") || !strings.Contains(view, "12 probes") {
		t.Fatalf("the page does not open at its rounds:\n%s", view)
	}
	// Down reads on, a line at a time, until the ask row shows.
	for i := 0; i < 60 && !strings.Contains(g.view(), "▸"); i++ {
		g.press("down")
	}
	view := g.view()
	if !strings.Contains(view, "▸") || strings.Contains(view, "12 probes") {
		t.Fatalf("down did not reach the ask row:\n%s", view)
	}
	for _, key := range []string{"home", "end"} {
		g.press(key)
		g.view()
	}
	g.press("home")
	if view := g.view(); !strings.Contains(view, "rounds") || !strings.Contains(view, "12 probes") {
		t.Errorf("home does not reach the rounds heading:\n%s", view)
	}
	g.press("end")
	g.view()
	for i := 0; i < 80 && !hasLine(g.view(), "rounds"); i++ {
		g.press("up")
	}
	if view := g.view(); !hasLine(view, "rounds") {
		t.Errorf("up does not reach the rounds heading:\n%s", view)
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
	// The page lines hold the row whole; the screen cuts a line wider than it.
	g.feed(jig2Mid()...).press("enter")
	for _, l := range pageLines(g) {
		if strings.Contains(l, "1234") && strings.Contains(l, "round 1") {
			if w := width(l) + 2; w > 80 {
				t.Errorf("a probe row is %d cells wide, over 80: %q", w, l)
			}
			if !strings.HasSuffix(l, "survived · caught in round 2") && !strings.HasSuffix(l, "caught") {
				t.Errorf("a probe row lost its verdict: %q", l)
			}
		}
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
