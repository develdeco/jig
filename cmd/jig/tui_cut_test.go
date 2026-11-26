package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestTUIPipeFramesCutNothingAt100: `jig tui` on a pipe prints its frames at
// 100 by 30, and an issue's plan shows a task's pull request into the issue's
// branch whole, no row is wider than the frame, and nothing is drawn with the
// emoji-capable cross.
func TestTUIPipeFramesCutNothingAt100(t *testing.T) {
	s := newSuitePublishRig(t, "")
	e := s.env()
	keys, out, wait := startTUI(t, e, "TK-1", "--store", s.root)
	frame := screenWith(t, out, "TK-2")
	frame = screenWith(t, out, "PR #2")
	for _, l := range strings.Split(frame, "\n") {
		if n := ansi.StringWidth(l); n > 100 {
			t.Errorf("a row is %d cells in a 100 wide frame: %q", n, l)
		}
	}
	if !strings.Contains(frame, "PR #2") || !strings.Contains(frame, s.branch) {
		t.Errorf("the plan does not name the pull request and the branch %s whole:\n%s", s.branch, frame)
	}
	if strings.Contains(out.String(), "✖") {
		t.Errorf("a frame draws the emoji-capable cross:\n%s", out.String())
	}
	keys.Write([]byte("q"))
	if wait() != 0 {
		t.Fatalf("jig tui TK-1 exited non-zero\n%s", out.String())
	}
}

// TestTUIPipeExpandsACutRow: `jig tui` on a pipe over a real store whose ticket
// has a title of 300 cells draws the row ending in a cut mark at 100 by 30;
// space lays the title out whole under its column in the next frame and space
// again gives back the row that ends in the mark.
func TestTUIPipeExpandsACutRow(t *testing.T) {
	r := newChartRig(t)
	r.ticket(t, "SHOP-1", "", false)
	words := make([]string, 60)
	for i := range words {
		words[i] = fmt.Sprintf("w%03d", i+1)
	}
	r.put(t, "tickets/SHOP-1/ticket.yaml", "schema_version: 1\ntitle: "+strings.Join(words, " ")+"\n")

	keys, out, wait := startTUI(t, r.env(), "--store", r.root)
	folded := screenWith(t, out, "w001")
	if !strings.Contains(folded, "…") || strings.Contains(folded, "w060") {
		t.Fatalf("the ticket's row is not cut in the first frame:\n%s", folded)
	}
	keys.Write([]byte(" "))
	open := screenWhere(t, out, "the title whole", func(f string) bool { return strings.Contains(f, "w060") })
	if strings.Contains(open, "…") {
		t.Errorf("space did not lay the title out whole:\n%s", open)
	}
	keys.Write([]byte(" "))
	again := screenWhere(t, out, "the folded row", func(f string) bool { return !strings.Contains(f, "w060") && strings.Contains(f, "…") })
	if again != folded {
		t.Errorf("space again does not give the first frame back:\n--- first\n%s\n--- again\n%s", folded, again)
	}
	keys.Write([]byte("q"))
	if wait() != 0 {
		t.Fatalf("jig tui exited non-zero\n%s", out.String())
	}
}
