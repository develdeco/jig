package tui

import (
	"strings"
	"testing"
)

// panelOKRig is the review screen after the triage of r1-f2 printed its help
// line.
func panelOKRig(t *testing.T) *rig {
	t.Helper()
	g := newQuietRig(t, "JIG-2")
	g.r.answer("triage JIG-2 r1-f2 dismiss --store /s", strings.Join([]string{
		"triage:", "  ticket: JIG-2", "  finding: r1-f2", "  decision: dismiss", "  provenance: person",
		"help[1]:", "  The next round applies it: run `jig gate JIG-2`",
	}, "\n")+"\n", 0)
	g.feed(jig2Mid()...).press("enter", "down", "d") // the review, then r1-f2
	return g
}

// boxLaid adds the lines of a row the allocator laid out, with no cut marks:
// the page tests build rows from words.
func (p *page) boxLaid(ref *rowRef, lines []string) {
	p.boxRow(ref, laidRow{lines: lines})
}
