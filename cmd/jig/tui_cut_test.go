package main

import (
	"strings"
	"testing"
	"unicode/utf8"
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
		if n := utf8.RuneCountInString(l); n > 100 {
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
