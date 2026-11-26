package tui

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// threeRoundsWith is threeRounds over a status the test names, with lines more.
func threeRoundsWith(t *testing.T, w, h int, status string, extra ...string) *rig {
	t.Helper()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", status, 0)
	evs := threeRoundsEvents(0)
	for i, d := range extra {
		evs = append(evs, jl(at(14, 40+i), "JIG-2", "", journal.EventTriage, with("s-person", "", d, "not a bug", "r1-f2", "person")))
	}
	g.feed(evs...).press("3")
	if g.m.Screen() != "review" {
		t.Fatalf("on %s, want review", g.m.Screen())
	}
	return g.sized(w, h)
}

// TestARoundSubStepOnTheTicketScreenOpensTheReviewOnItsRow: a round's sub-step on the
// ticket screen names its round, and enter opens the review with the Rounds cursor
// on that round, not on the latest.
func TestARoundSubStepOnTheTicketScreenOpensTheReviewOnItsRow(t *testing.T) {
	t.Parallel()
	g := threeRounds(t, 100, 40, 0)
	g.m.back()
	if g.m.Screen() != "ticket" {
		t.Fatalf("on %s, want the ticket screen", g.m.Screen())
	}
	g.m.cursorToRow(func(r rowRef) bool { return r.kind == rowReview })
	g.view()
	g.m.cursorToRow(func(r rowRef) bool { return r.kind == rowSub && r.id == "round:1" })
	ref := g.m.selected()
	if ref == nil || ref.kind != rowSub || ref.round != "1" {
		t.Fatalf("the cursor is on %+v, want the sub-step of round 1 naming its round", ref)
	}
	g.press("enter")
	if g.m.Screen() != "review" {
		t.Fatalf("enter opened %s, want the review", g.m.Screen())
	}
	g.view()
	if ref := g.m.selected(); ref == nil || ref.kind != rowRound || ref.id != "1" {
		t.Errorf("the review opens with the cursor on %+v, want round 1", ref)
	}
	if !strings.Contains(g.view(), "╭─ Detail · round 1") {
		t.Errorf("the Detail is not round 1's:\n%s", g.view())
	}
}

// TestADecidedAskOfAnEarlierRoundDoesNotSayItWaits: a finding round 1 recorded as
// an ask and a person decided, which a later round applied, says what was decided
// and that it was applied, not that it waits for a person.
func TestADecidedAskOfAnEarlierRoundDoesNotSayItWaits(t *testing.T) {
	t.Parallel()
	status := strings.Replace(threeRoundsStatus("r1-f1"), "r1-f2,1,note,medium", "r1-f2,1,ask,medium", 1)
	status = strings.Replace(status, "Rename the helper,noted", "Rename the helper,asked", 1)
	g := threeRoundsWith(t, 100, 40, status, "dismiss")
	g.cursorOn(rowRound, "1").cursorOn(rowFinding, "r1-f2")
	g.press("enter")
	if g.m.Screen() != "finding" {
		t.Fatalf("on %s, want the finding", g.m.Screen())
	}
	view := readWords(g.view())
	if strings.Contains(view, "waits for a person") || strings.Contains(view, "! ask") {
		t.Errorf("a decided ask says it waits:\n%s", g.view())
	}
	if !strings.Contains(view, "dismissed by you") || !strings.Contains(view, "a later round applied it") {
		t.Errorf("the screen does not say what was decided and that it was applied:\n%s", g.view())
	}
}

// TestARoundOrATryNamedFromAnotherScreenOpensWithTheCursorOnItWhateverZoomOrSearchItWasLeftWith:
// a round named by a slice's Finding panel, a carried round, a try named by the
// build screen: the screen opens with its cursor on the row, drawn in the first
// frame, though it was left zoomed or narrowed by a search.
func TestARoundOrATryNamedFromAnotherScreenOpensWithTheCursorOnItWhateverZoomOrSearchItWasLeftWith(t *testing.T) {
	t.Parallel()
	onRow := func(g *rig, kind, id string) {
		t.Helper()
		if ref := g.m.selected(); ref == nil || ref.kind != kind || ref.id != id {
			t.Fatalf("the cursor is on %+v, want %s %s:\n%s", ref, kind, id, g.view())
		}
	}
	// The review left with a search that hides round 1, opened on round 1 from a slice.
	g := threeRounds(t, 100, 30, 0)
	g.tabTo("Rounds")
	g.press("/")
	typeText(g, "clean")
	g.press("enter")
	g.onSlice("fix-1-alpha")
	g.tabTo("Finding")
	g.cursorOn(rowRound, "1")
	g.press("enter")
	if g.m.Screen() != "review" {
		t.Fatalf("on %s, want the review", g.m.Screen())
	}
	first := g.view()
	onRow(g, rowRound, "1")
	if !strings.Contains(first, "▸ ! round 1") {
		t.Errorf("the first frame does not draw the cursor on round 1:\n%s", first)
	}

	// The carried row under a search that hides the round it names.
	c := threeRounds(t, 100, 30, 2)
	c.cursorOn(rowRound, "3")
	c.tabTo("Rounds")
	c.press("/")
	typeText(c, "clean")
	c.press("enter")
	c.cursorOn(rowCarry, "2")
	c.press("enter")
	onRow(c, rowRound, "2")

	// A try named by the build screen, the slice left zoomed.
	z := fiveTries(t, 100, 30)
	z.cursorOn(rowSlice, "a")
	z.press("enter")
	z.tabTo("Slice a")
	z.press("z")
	z.m.back()
	z.cursorOn(rowSlice, "a")
	z.tabTo("Detail · a")
	z.cursorOn(rowTry, sha('4'))
	z.press("enter")
	if z.m.Screen() != "slice" {
		t.Fatalf("on %s, want the slice", z.m.Screen())
	}
	frame := z.view()
	onRow(z, rowTry, sha('4'))
	if z.m.top().zoom != "" || !strings.Contains(frame, "▸ ✗ 4444444") {
		t.Errorf("the first frame does not draw the cursor on the try:\n%s", frame)
	}
}

// longGoalStatus is threeRoundsStatus whose first fix slice has a goal of many lines.
func longGoalStatus() string {
	more := ""
	for i := 1; i <= 24; i++ {
		more += "\\nGoal line " + strconv.Itoa(i) + " says what the builder must do about it."
	}
	return strings.Replace(threeRoundsStatus("r1-f1"), `alpha/a.go:12 Close the file"`, `alpha/a.go:12 Close the file`+more+`\nlast line of the goal"`, 1)
}

// TestATryOpenedFromTheBuildDetailLandsOnTheTry: a fix slice with a long Finding
// panel opened on one of its tries draws the try and its cursor in the first
// frame, at the sizes where the panel alone fills the screen.
func TestATryOpenedFromTheBuildDetailLandsOnTheTry(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{80, 24}, {100, 30}, {160, 40}} {
		g := threeRoundsWith(t, size[0], size[1], longGoalStatus())
		g.press("2")
		g.cursorOn(rowSlice, "fix-1-alpha")
		g.tabTo("Detail · fix-1-alpha")
		g.cursorOn(rowTry, sha('b'))
		g.press("enter")
		if g.m.Screen() != "slice" {
			t.Fatalf("%dx%d: on %s, want the slice", size[0], size[1], g.m.Screen())
		}
		frame := g.view()
		if !strings.Contains(frame, "▸ ✗ bbbbbbb") {
			t.Errorf("%dx%d: the first frame does not draw the try with its cursor:\n%s", size[0], size[1], frame)
		}
	}
}

// TestAPaneOfRowsAndTextReadsToItsEnd: a Finding panel of rows followed by the
// goal's text is read to its last line by down, and zoomed it is whole.
func TestAPaneOfRowsAndTextReadsToItsEnd(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{80, 24}, {100, 30}} {
		g := threeRoundsWith(t, size[0], size[1], longGoalStatus())
		g.onSlice("fix-1-alpha")
		g.tabTo("Finding")
		for i := 0; i < 40; i++ {
			g.press("down")
		}
		if got := readWords(g.view()); !strings.Contains(got, "last line of the goal") {
			t.Errorf("%dx%d: down never reaches the end of the Finding panel:\n%s", size[0], size[1], g.view())
		}
		g.press("z")
		if zoomed := readWords(g.view()); !strings.Contains(zoomed, "Goal line 24 says") || !strings.Contains(zoomed, "last line of the goal") {
			t.Errorf("%dx%d: the zoomed Finding panel does not end where the text does:\n%s", size[0], size[1], g.view())
		}
		g.press("home")
		if zoomed := readWords(g.view()); !strings.Contains(zoomed, "Goal line 1 says") {
			t.Errorf("%dx%d: the zoomed Finding panel does not read back to its first line:\n%s", size[0], size[1], g.view())
		}
	}
}

// TestBracketsThenEscReturnsToTheFindingShown: stepping to a sibling finding with
// ] and going back lands on the finding that was on screen.
func TestBracketsThenEscReturnsToTheFindingShown(t *testing.T) {
	t.Parallel()
	g := threeRounds(t, 100, 30, 0)
	g.cursorOn(rowRound, "1").cursorOn(rowFinding, "r1-f1")
	g.press("enter", "]", "]")
	if g.m.top().finding != "r1-f3" {
		t.Fatalf("on %q, want r1-f3", g.m.top().finding)
	}
	g.press("esc")
	if ref := g.m.selected(); ref == nil || ref.kind != rowFinding || ref.id != "r1-f3" {
		t.Errorf("esc lands on %+v, want the finding shown, r1-f3", ref)
	}
}

// TestEscAfterASiblingStepLandsOnTheSiblingsRow: the same for the slices of the
// build screen: ] to the next slice and esc leave the cursor on that slice.
func TestEscAfterASiblingStepLandsOnTheSiblingsRow(t *testing.T) {
	t.Parallel()
	g := threeRounds(t, 100, 30, 0)
	g.onSlice("a")
	g.press("]")
	if g.m.top().slice != "fix-1-alpha" {
		t.Fatalf("] opened %q, want fix-1-alpha", g.m.top().slice)
	}
	g.press("esc")
	if ref := g.m.selected(); g.m.Screen() != "build" || ref == nil || ref.kind != rowSlice || ref.id != "fix-1-alpha" {
		t.Errorf("esc lands on %s %+v, want the build screen on fix-1-alpha", g.m.Screen(), ref)
	}
}

var marksRE = regexp.MustCompile(`[↑↓] (\d+) more`)

// TestAFocusedDetailMarksEveryLineItCuts: with the focus in a round's Detail every
// line of it is drawn or counted in a mark, at the floor and at the common sizes.
func TestAFocusedDetailMarksEveryLineItCuts(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{60, 20}, {80, 24}, {100, 30}} {
		g := threeRounds(t, size[0], size[1], 0)
		g.cursorOn(rowRound, "1").tabTo("Detail · round 1")
		inner := func() []string {
			var out []string
			in := false
			for _, l := range strings.Split(g.view(), "\n") {
				switch {
				case strings.Contains(l, "╭─ Detail"):
					in = true
				case strings.Contains(l, "╰"):
					in = false
				case in:
					out = append(out, l)
				}
			}
			return out
		}
		tk := g.m.w.tickets["JIG-2"]
		var round roundRow
		for _, r := range withStatusRounds(tk, tk.rounds(g.m.zone)) {
			if r.N == 1 {
				round = r
			}
		}
		total := len(g.m.detailLines(g.m.roundDetail(tk, round), g.m.width)) - 2
		shown, counted := 0, 0
		for _, l := range inner() {
			if m := marksRE.FindStringSubmatch(l); m != nil {
				n, _ := strconv.Atoi(m[1])
				counted += n
				continue
			}
			shown++
		}
		if shown+counted != total {
			t.Errorf("%dx%d: the Detail has %d lines, %d drawn and %d counted in marks:\n%s", size[0], size[1], total, shown, counted, g.view())
		}
	}
}
