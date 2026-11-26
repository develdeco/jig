package tui

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// A 600-cell title is 120 numbered words: the narrowest screens cannot hold it
// whole in their body.
const tallWords = 120

// atCursor is the id of the row the cursor stands on.
func atCursor(g *rig) string {
	g.t.Helper()
	g.view()
	r := g.m.refIn(g.m.page())
	if r == nil {
		return ""
	}
	return r.id
}

var moreLines = regexp.MustCompile(`([↑↓]) (\d+) more lines?`)

// linesHeld is how many lines of the row the frame says are above (arrow ↑) or
// below (↓) the part it shows, -1 when it says none. The panel's own count of
// rows is not a count of lines.
func linesHeld(frame, arrow string) int {
	for _, m := range moreLines.FindAllStringSubmatch(frame, -1) {
		if m[1] == arrow {
			n, _ := strconv.Atoi(m[2])
			return n
		}
	}
	return -1
}

// TestATallExpandedRowIsReadByLines: an expanded row that the window cannot hold
// is drawn from its first line, with a count of the lines below it; down moves
// the part a line, with a count of the lines above, until its last word shows,
// and the cursor stays on the row all the way.
func TestATallExpandedRowIsReadByLines(t *testing.T) {
	t.Parallel()
	g := cutRig(t, 60, 20, tallWords).onRow("JIG-1")
	g.press("space")
	frame := stripANSI(g.view())
	if !strings.Contains(frame, "w001") || strings.Contains(frame, "w120") {
		t.Fatalf("the row does not start at its first line:\n%s", frame)
	}
	if linesHeld(frame, "↓") <= 0 || linesHeld(frame, "↑") != -1 {
		t.Errorf("the part shown does not count the lines below it only:\n%s", frame)
	}
	g.press("down")
	one := stripANSI(g.view())
	if linesHeld(one, "↑") != 1 || strings.Contains(one, "w001") || !strings.Contains(one, "w006") {
		t.Errorf("one press down did not move the part a line:\n%s", one)
	}
	if atCursor(g) != "JIG-1" {
		t.Errorf("the cursor left the row it is reading")
	}
	for i := 0; i < 80 && !strings.Contains(stripANSI(g.view()), "w120"); i++ {
		g.press("down")
		if atCursor(g) != "JIG-1" {
			t.Fatalf("the cursor left the row before its last word was shown:\n%s", g.view())
		}
	}
	last := stripANSI(g.view())
	if !strings.Contains(last, "w120") || linesHeld(last, "↓") != -1 || linesHeld(last, "↑") <= 0 {
		t.Errorf("the row's last line is not shown with only the lines above counted:\n%s", last)
	}
	if got := len(strings.Split(last, "\n")); got != 20 {
		t.Errorf("the frame has %d lines, want 20", got)
	}
	// Up reads back to the first line.
	for i := 0; i < 80 && linesHeld(stripANSI(g.view()), "↑") != -1; i++ {
		g.press("up")
	}
	if back := stripANSI(g.view()); !strings.Contains(back, "w001") || atCursor(g) != "JIG-1" {
		t.Errorf("up did not read back to the row's first line:\n%s", back)
	}
}

// TestAPressPastTheRowsEndMovesToTheNeighbour: a press down at the row's last
// line, or up at its first, moves the cursor to the neighbouring row, which is
// read from its own first line.
func TestAPressPastTheRowsEndMovesToTheNeighbour(t *testing.T) {
	t.Parallel()
	g := cutRig(t, 60, 20, tallWords).onRow("JIG-1")
	g.press("space")
	for i := 0; i < 80 && !strings.Contains(stripANSI(g.view()), "w120"); i++ {
		g.press("down")
	}
	if !strings.Contains(stripANSI(g.view()), "w120") || atCursor(g) != "JIG-1" {
		t.Fatalf("the row's last line is not reached:\n%s", g.view())
	}
	g.press("down")
	if atCursor(g) != "JIG-2" {
		t.Fatalf("a press past the last line left the cursor on %q, want JIG-2", atCursor(g))
	}
	g.press("space", "down")
	if atCursor(g) != "JIG-2" || linesHeld(stripANSI(g.view()), "↑") != 1 {
		t.Fatalf("the neighbour is not read by lines from its first:\n%s", g.view())
	}
	g.press("up")
	if atCursor(g) != "JIG-2" || linesHeld(stripANSI(g.view()), "↑") != -1 {
		t.Fatalf("up did not read back to the neighbour's first line:\n%s", g.view())
	}
	g.press("up")
	if atCursor(g) != "JIG-1" {
		t.Errorf("a press up past the first line left the cursor on %q, want JIG-1", atCursor(g))
	}
	if frame := stripANSI(g.view()); !strings.Contains(frame, "w001") || linesHeld(frame, "↑") != -1 {
		t.Errorf("the row before is not read from its first line:\n%s", frame)
	}
}

// TestAZoomedTallRowShowsTheWholeBody: a pane that is zoomed has the whole body
// for its rows, so a row too tall for it is read in a part as long as the body
// allows, with no other panel taking lines.
func TestAZoomedTallRowShowsTheWholeBody(t *testing.T) {
	t.Parallel()
	g := sized(longChartRig(t, 120), 80, 30).openChart("checkout").onRow("JIG-8")
	g.press("space")
	g.view()
	if g.m.tall == nil {
		t.Fatalf("the row is not read by lines in its pane:\n%s", g.view())
	}
	part := g.m.tall.part
	g.press("z")
	frame := stripANSI(g.view())
	if g.m.tall == nil || g.m.tall.part < part {
		t.Fatalf("the zoomed pane holds a shorter part than the pane did:\n%s", frame)
	}
	if strings.Contains(frame, "╭─ Detail") || strings.Contains(frame, "╭─ Activity") {
		t.Errorf("another panel takes lines of the zoomed body:\n%s", frame)
	}
	// The part is the body less the pane's edges and names.
	body := len(strings.Split(frame, "\n")) - 2 - 3
	if g.m.tall.part < body-6 {
		t.Errorf("the part holds %d lines of a body of %d:\n%s", g.m.tall.part, body, frame)
	}
	g.press("z")
	if frame := stripANSI(g.view()); g.m.tall == nil || strings.Contains(frame, "zoomed") {
		t.Errorf("unzooming left the pane zoomed:\n%s", frame)
	}
}

// TestAResizeLaysAnExpandedRowOutAgain: a resize lays the row out at the new
// width, whole when the body now holds it, and clamps the lines held above the
// part when the body is taller or the row is shorter.
func TestAResizeLaysAnExpandedRowOutAgain(t *testing.T) {
	t.Parallel()
	g := cutRig(t, 60, 20, tallWords).onRow("JIG-1")
	g.press("space")
	g.press("down", "down", "down", "down", "down", "down", "down", "down")
	if got := linesHeld(stripANSI(g.view()), "↑"); got != 8 {
		t.Fatalf("the part is %d lines down, want 8:\n%s", got, g.view())
	}
	g.send(tea.WindowSizeMsg{Width: 230, Height: 50})
	wide := stripANSI(g.view())
	if g.m.tall != nil || linesHeld(wide, "↑") != -1 || !hasWordsInOrder(blockOf(wide, "JIG-1"), cutTitle(tallWords)) {
		t.Errorf("a body that holds the row does not show it whole:\n%s", wide)
	}
	g.send(tea.WindowSizeMsg{Width: 60, Height: 20})
	back := stripANSI(g.view())
	if g.m.tall == nil || atCursor(g) != "JIG-1" {
		t.Errorf("the narrow screen does not read the row by lines again:\n%s", back)
	}
	// A resize to a taller body moves the held lines no further than the row has.
	g.send(tea.WindowSizeMsg{Width: 60, Height: 28})
	if got := linesHeld(stripANSI(g.view()), "↑"); g.m.tall != nil && got > g.m.tall.maxScroll() {
		t.Errorf("%d lines held above a part that can have at most %d", got, g.m.tall.maxScroll())
	}
	// A press the whole way down after the resize still ends on the last word.
	for i := 0; i < 120 && !strings.Contains(stripANSI(g.view()), "w120"); i++ {
		g.press("down")
	}
	if !strings.Contains(stripANSI(g.view()), "w120") || atCursor(g) != "JIG-1" {
		t.Errorf("the last line is not reached after a resize:\n%s", g.view())
	}
}
