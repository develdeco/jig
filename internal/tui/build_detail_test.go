package tui

import (
	"strings"
	"testing"
)

// fixRoundBuild is the build screen of the ticket in its fix round at w x h, the
// cursor on its first slice.
func fixRoundBuild(t *testing.T, w, h int) *rig {
	t.Helper()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", fixRoundStatus(), 0)
	g.feed(jig1Records()...).press("enter")
	if g.m.Screen() != "build" {
		t.Fatalf("on %s, want build", g.m.Screen())
	}
	return g.sized(w, h)
}

// rowHolding is the first line of the view with every one of words in it, "" when
// there is none.
func rowHolding(view string, words ...string) string {
next:
	for _, l := range strings.Split(view, "\n") {
		for _, w := range words {
			if !strings.Contains(l, w) {
				continue next
			}
		}
		return l
	}
	return ""
}

// TestADetailPanelShowsEveryFieldWholeAndMarksWhatIsCut: a field wraps at words
// and loses none of them, however long, and a detail taller than the room the
// screen leaves is windowed inside its box with the mark of what is below.
func TestADetailPanelShowsEveryFieldWholeAndMarksWhatIsCut(t *testing.T) {
	t.Parallel()
	words := strings.Fields("one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen twenty")
	long := strings.Join(words, " ")
	g := newRig(t, "JIG-1")
	d := detailSpec{title: "a", summary: "green", rows: []detailRow{
		{label: "goal", text: long},
		{label: "oracle", text: "go test -count=1 ./internal/tui -run 'TestOne|TestTwo|TestThree|TestFour'"},
		{indent: 2, icon: StateOK, text: long},
	}}
	g.m.width = 40
	var all []string
	for _, l := range g.m.detailLines(d, 40) {
		if l.box == boxMid {
			all = append(all, l.text)
			if width(l.text) > 33 {
				t.Errorf("a field line is %d cells in a box with 33: %q", width(l.text), l.text)
			}
		}
	}
	got := readWords(strings.Join(all, "\n"))
	for _, w := range append(words, "TestFour'") {
		if !strings.Contains(got, w) {
			t.Errorf("the detail lost %q:\n%s", w, strings.Join(all, "\n"))
		}
	}
	if strings.Contains(got, "…") {
		t.Errorf("the detail cut a field short:\n%s", strings.Join(all, "\n"))
	}

	// A screen with too few rows for the detail windows it inside its box.
	b := fixRoundBuild(t, 80, 20)
	b.press("down", "down", "down", "down") // the fix slice, whose goal is long
	view := b.view()
	if detail := between(view, "╭─ Detail · fix-2-app", "╰"); !strings.Contains(detail, "│  ↓ ") || strings.Contains(detail, "…") {
		t.Errorf("the detail is not cut with a mark inside its box:\n%s", view)
	}
	if h := len(strings.Split(view, "\n")); h != 20 {
		t.Errorf("the screen is %d rows, want 20:\n%s", h, view)
	}
}

// TestTheDetailSitsUnderTheListAtEveryWidth: from 80 to 230 columns the detail is
// the panel under the list, as wide as the screen allows, and no line is wider than
// the terminal.
func TestTheDetailSitsUnderTheListAtEveryWidth(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{80, 24}, {159, 40}, {160, 40}, {230, 50}} {
		w, h := size[0], size[1]
		view := fixRoundBuild(t, w, h).view()
		if rowHolding(view, "Slices", "╭─ Detail · a") != "" || rowHolding(view, "╭─ Detail · a") == "" {
			t.Errorf("at %d columns the detail is not the panel under the list:\n%s", w, view)
		}
		if list, detail := strings.Index(view, "╭─ Slices"), strings.Index(view, "╭─ Detail · a"); list < 0 || detail < list {
			t.Errorf("at %d columns the detail does not come after the list:\n%s", w, view)
		}
		for _, l := range strings.Split(view, "\n") {
			if width(l) > w {
				t.Errorf("a line is %d cells at %d columns: %q", width(l), w, l)
			}
		}
	}
}

// TestBuildScreenIsARowPerSliceAndTheCursorSliceWhole: at 100x30 a ticket of five
// slices has a row for each, the fix slice's goal whole in its detail under the
// list with the oracle once, and the Sessions panel in the first screenful.
func TestBuildScreenIsARowPerSliceAndTheCursorSliceWhole(t *testing.T) {
	t.Parallel()
	g := fixRoundBuild(t, 100, 30)
	view := g.view()
	list := between(view, "Slices", "╰")
	for _, id := range []string{"a", "b", "c", "d", "fix-2-app"} {
		if !strings.Contains(list, " "+id+" ") {
			t.Errorf("the list has no row for slice %q:\n%s", id, view)
		}
	}
	if !strings.Contains(readWords(between(view, "╭─ Detail · a ", "╰")), "goal Greet by name oracle unit workspace app") {
		t.Errorf("the first screenful does not hold the detail of the first slice whole:\n%s", view)
	}
	if sessions := between(view, "Sessions", "╰"); !strings.Contains(sessions, "builder") {
		t.Errorf("the first screenful has no Sessions row under its heading:\n%s", view)
	}
	if h := len(strings.Split(view, "\n")); h != 30 {
		t.Errorf("the screen is %d rows, want 30", h)
	}

	// A slice checked twice and failed: its oracle is said once, in the detail,
	// and each try is a row of it.
	g.press("down")
	view = g.view()
	detail := between(view, "╭─ Detail · b ", "╰")
	if got := strings.Count(detail, "go test ./..."); got != 1 {
		t.Errorf("the detail says the oracle %d times, want once:\n%s", got, view)
	}
	for _, want := range []string{"goal Say goodbye, politely", "workspace app", "eeeeeee “Say goodbye, politely”", "bbbbbbb “Say goodbye”", "--- FAIL: TestB"} {
		if !strings.Contains(readWords(detail), want) {
			t.Errorf("the detail of b lacks %q:\n%s", want, view)
		}
	}
}

// TestAFixSliceRowReadsAsItsFindingByRound: a fix slice's goal column says the
// round it came from and the line of its finding, and its detail the whole goal
// with the round and the finding ids.
func TestAFixSliceRowReadsAsItsFindingByRound(t *testing.T) {
	t.Parallel()
	g := fixRoundBuild(t, 100, 40)
	view := g.view()
	if !strings.Contains(readWords(between(view, "Slices", "╰")), "round 2 · publish.go:1105 The stacked parent's fallback is no longer reached") {
		t.Errorf("the fix slice's row does not read as its finding by round:\n%s", view)
	}
	g.press("down", "down", "down", "down")
	detail := readWords(between(g.view(), "╭─ Detail · fix-2-app", "╰"))
	for _, want := range []string{"A stale parent would merge.", "Risk (high): the fallback is the only guard.", "round 2 · r1-f1"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the fix slice's detail lacks %q:\n%s", want, detail)
		}
	}
}

// TestTheBuildDetailFollowsTheCursor: each move of the cursor changes the detail
// to the slice it lands on, and it needs no key of its own.
func TestTheBuildDetailFollowsTheCursor(t *testing.T) {
	t.Parallel()
	g := fixRoundBuild(t, 100, 40)
	for _, want := range []struct{ id, text string }{
		{"a", "Greet by name"}, {"b", "Say goodbye, politely"}, {"c", "Pick the tone"}, {"d", "Ship the copy"}, {"fix-2-app", "publish.go:1105"},
	} {
		view := g.view()
		detail := between(view, "╭─ Detail · "+want.id+" ", "╰")
		if detail == "" || !strings.Contains(readWords(detail), want.text) {
			t.Errorf("the detail of the slice under the cursor is not %s's:\n%s", want.id, view)
		}
		if strings.Count(view, "╭─ Detail · "+want.id+" ") != 1 {
			t.Errorf("the detail is drawn %d times:\n%s", strings.Count(view, "╭─ Detail · "+want.id+" "), view)
		}
		g.press("down")
	}
	g.press("up")
	if !strings.Contains(g.view(), "╭─ Detail · d ") {
		t.Errorf("up does not take the detail back to d:\n%s", g.view())
	}
}
