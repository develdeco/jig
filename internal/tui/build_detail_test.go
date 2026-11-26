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
	b := fixRoundBuild(t, 80, 16)
	b.press("down", "down", "down", "down") // the fix slice, whose goal is long
	view := b.view()
	if line := rowHolding(view, "more"); !strings.HasPrefix(strings.TrimSpace(line), "│") || !strings.HasSuffix(strings.TrimSpace(line), "│") {
		t.Errorf("the mark of what is cut is not inside a box:\n%s", view)
	}
	if h := len(strings.Split(view, "\n")); h != 16 {
		t.Errorf("the screen is %d rows, want 16:\n%s", h, view)
	}
}

// TestTheDetailSitsBesideTheListFrom160Columns: from 160 columns the list takes
// three fifths of the width and the detail the rest, both boxes drawn as tall as
// the taller; under it the detail is the panel below the list.
func TestTheDetailSitsBesideTheListFrom160Columns(t *testing.T) {
	t.Parallel()
	for _, w := range []int{160, 230} {
		view := fixRoundBuild(t, w, 40).view()
		top := rowHolding(view, "Slices", "╭─ Detail · a")
		if top == "" {
			t.Fatalf("at %d columns the detail is not beside the list:\n%s", w, view)
		}
		// The two boxes are two cells apart: the list is three fifths of the width.
		if list := width(strings.Split(top, "╭─ Detail · a")[0]) - 2; list != w*3/5 {
			t.Errorf("at %d columns the list is %d cells wide, want %d", w, list, w*3/5)
		}
		lines := strings.Split(view, "\n")
		for _, l := range lines {
			if width(l) > w {
				t.Errorf("a line is %d cells at %d columns: %q", width(l), w, l)
			}
		}
		if bottom := rowHolding(view, "╰", "╯  ╰"); bottom == "" {
			t.Errorf("at %d columns the two boxes do not end on one line:\n%s", w, view)
		}
		// The list's rows and the detail's fields share lines.
		if row := rowHolding(view, "▸ ● a", "oracle"); row == "" {
			t.Errorf("at %d columns the cursor's row has no field beside it:\n%s", w, view)
		}
	}
	under := fixRoundBuild(t, 159, 40).view()
	if rowHolding(under, "Slices", "╭─ Detail · a") != "" || rowHolding(under, "╭─ Detail · a") == "" {
		t.Errorf("at 159 columns the detail is not the panel below the list:\n%s", under)
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
