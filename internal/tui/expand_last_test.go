package tui

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// longSentence is a sentence a verb prints of 190 cells, which no 80 wide line
// holds beside its command.
var longSentence = strings.TrimSpace(strings.Repeat("The next round applies it and reads what the person decided before it opens. ", 3))[:190]

// triagedWith is the review screen after a triage whose sentence is longSentence,
// with the cursor on the ask r1-f2, at w by h.
func triagedWith(t *testing.T, w, h int) *rig {
	t.Helper()
	g := newQuietRig(t, "JIG-2")
	g.r.answer("triage JIG-2 r1-f2 dismiss --store /s", strings.Join([]string{
		"triage:", "  ticket: JIG-2", "  finding: r1-f2", "  decision: dismiss", "  provenance: person",
		"help[1]:", "  " + longSentence,
	}, "\n")+"\n", 0)
	g.send(tea.WindowSizeMsg{Width: w, Height: h})
	g.feed(jig2Mid()...).press("enter", "down", "d")
	if g.m.last == nil {
		t.Fatalf("the triage left no last command:\n%s", g.view())
	}
	return g
}

// lastBlock is the rows of the frame the last command takes: from its command to
// the rule over the keys.
func lastBlock(frame string) string {
	lines := strings.Split(stripANSI(frame), "\n")
	for i, l := range lines {
		if strings.Contains(l, "jig triage JIG-2 r1-f2 dismiss") {
			for j := i + 1; j < len(lines); j++ {
				if strings.HasPrefix(lines[j], "───") {
					return strings.Join(lines[i:j], "\n")
				}
			}
		}
	}
	return ""
}

// TestTheLastCommandLineExpandsWhole: a last command cut by the width ends in a
// mark; space (the focus holds nothing cut) wraps it whole under its own column,
// and a click on the mark does the same.
func TestTheLastCommandLineExpandsWhole(t *testing.T) {
	t.Parallel()
	g := triagedWith(t, 80, 24)
	before := stripANSI(g.view())
	if block := lastBlock(before); !strings.Contains(block, "…") || strings.Contains(block, "reads what the person") && hasWordsInOrder(block, longSentence) {
		t.Fatalf("the last command is not cut:\n%s", before)
	}
	if hint := strings.Join(strings.Fields(before), " "); !strings.Contains(hint, "space expand") {
		t.Errorf("the hint line does not offer space for the last command:\n%s", before)
	}
	g.press("space")
	open := stripANSI(g.view())
	block := lastBlock(open)
	if strings.Contains(block, "…") || !hasWordsInOrder(block, longSentence) || !strings.Contains(block, "jig triage JIG-2 r1-f2 dismiss") {
		t.Errorf("space did not wrap the whole line:\n%s", open)
	}
	// The words wrap under their own column: every row after the first starts
	// where the first row's words do.
	rows := strings.Split(block, "\n")
	at := len([]rune(rows[0][:strings.Index(rows[0], "The next round")]))
	for _, r := range rows[1:] {
		if lead := len([]rune(r)) - len([]rune(strings.TrimLeft(r, " "))); lead != at {
			t.Errorf("a row of the wrapped line starts at cell %d, want %d (under its column): %q", lead, at, r)
		}
	}
	if hint := strings.Join(strings.Fields(open), " "); !strings.Contains(hint, "space fold") {
		t.Errorf("the hint line does not offer space to fold:\n%s", open)
	}
	if got := len(strings.Split(open, "\n")); got != 24 {
		t.Errorf("the frame has %d lines, want 24", got)
	}

	// A click on the mark expands it as space does.
	g = triagedWith(t, 80, 24)
	frame := stripANSI(g.view())
	lines := strings.Split(frame, "\n")
	for y, l := range lines {
		if at := strings.Index(l, "…"); at >= 0 && strings.Contains(l, "jig triage") {
			leftClick(g, len([]rune(l[:at])), y)
			break
		}
	}
	if clicked := stripANSI(g.view()); strings.Contains(lastBlock(clicked), "…") || !hasWordsInOrder(lastBlock(clicked), longSentence) {
		t.Errorf("a click on the mark did not expand the line:\n%s", clicked)
	}
}

// TestSpaceReachesTheLastCommandOnlyWhenTheFocusHoldsNoCut: a row of the focused
// pane that is cut is `space`'s first; the last command is reached when the focus
// holds nothing cut, and the keys say which.
func TestSpaceReachesTheLastCommandOnlyWhenTheFocusHoldsNoCut(t *testing.T) {
	t.Parallel()
	// At 60 wide the ask row's own cells are cut: space takes the row.
	g := triagedWith(t, 60, 40)
	g.view()
	if !g.m.keysOn.cut {
		t.Fatalf("the row under the cursor is not cut at 60 wide:\n%s", g.view())
	}
	before := lastBlock(g.view())
	g.press("space")
	if got := expandedIn(g); got != 1 {
		t.Fatalf("space did not expand the row first: %d expanded", got)
	}
	if lastBlock(g.view()) != before {
		t.Errorf("space expanded the last command while the row held a cut:\n%s", g.view())
	}
	// With the row expanded the focus holds an expansion: space folds it.
	g.press("space")
	if expandedIn(g) != 0 {
		t.Errorf("space did not fold the row")
	}

	// A row with nothing cut leaves space to the last command.
	g = triagedWith(t, 100, 40)
	g.view()
	if g.m.keysOn.cut {
		t.Fatalf("the row is cut at 100 wide:\n%s", g.view())
	}
	g.press("space")
	if expandedIn(g) != 0 || strings.Contains(lastBlock(g.view()), "…") {
		t.Errorf("space did not reach the last command when the focus held nothing cut:\n%s", g.view())
	}
}

// TestAFoldedLastCommandIsAsItWas: space again folds the line to the frame it
// had, and a command that replaces it is cut again.
func TestAFoldedLastCommandIsAsItWas(t *testing.T) {
	t.Parallel()
	g := triagedWith(t, 80, 24)
	before := stripANSI(g.view())
	g.press("space")
	g.press("space")
	if folded := stripANSI(g.view()); folded != before {
		t.Errorf("folding does not give the frame back:\n--- before\n%s\n--- after\n%s", before, folded)
	}
	g.press("space")
	open := stripANSI(g.view())
	if strings.Contains(lastBlock(open), "…") {
		t.Fatalf("the line did not expand again:\n%s", open)
	}
	// A new command replaces the line: it opens folded.
	replaced := *g.m.last
	g.m.last = &replaced
	if g.m.last == nil || g.m.lastOpen == g.m.last {
		t.Errorf("a new command opened expanded")
	}
}

// TestALineTooTallForTheScreenSaysWhatItNeeds: a line that needs more rows than
// the screen can spare while the body keeps its minimum is not expanded, and the
// notice says how many it needs and how many the screen has.
func TestALineTooTallForTheScreenSaysWhatItNeeds(t *testing.T) {
	t.Parallel()
	g := triagedWith(t, 80, 24)
	g.send(tea.WindowSizeMsg{Width: 80, Height: 13})
	g.view()
	before := stripANSI(g.view())
	g.press("space")
	view := stripANSI(g.view())
	want := regexp.MustCompile(`the whole line needs \d+ rows, this screen has \d+`)
	if !want.MatchString(view) {
		t.Fatalf("the notice does not say what the line needs:\n%s", view)
	}
	if g.m.lastOpen != nil {
		t.Errorf("the line expanded on a screen with no room for it")
	}
	if strings.Contains(before, "the whole line") {
		t.Errorf("the notice was there before the key")
	}
}
